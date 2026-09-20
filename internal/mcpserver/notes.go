package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danPoku/ctx/internal/store"
	"github.com/danPoku/ctx/queries"
)

// validNoteKinds mirrors notes.kind's CHECK constraint in
// migrations/001_core.sql — validated here too so a bad kind comes back as
// a clear tool error instead of an opaque SQLite constraint failure.
var validNoteKinds = map[string]bool{
	"decision": true, "gotcha": true, "convention": true, "todo": true, "fact": true,
}

type saveNoteArgs struct {
	Kind  string   `json:"kind" jsonschema:"one of: decision, gotcha, convention, todo, fact"`
	Title string   `json:"title" jsonschema:"short title for this note"`
	Body  string   `json:"body,omitempty" jsonschema:"the note's full content"`
	Tags  []string `json:"tags,omitempty" jsonschema:"free-form tags for filtering later"`
}

type saveNoteResult struct {
	ID int64 `json:"id"`
}

func (s *server) registerNotes(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "save_note",
		Description: "Record a deliberate note for this project — a decision, gotcha, convention, todo, or fact worth remembering across sessions and agents. " +
			"For things worth writing down on purpose, not a transcript of what happened (that's captured automatically by ingest).",
	}, s.saveNote)
}

func (s *server) saveNote(_ context.Context, req *mcp.CallToolRequest, args saveNoteArgs) (*mcp.CallToolResult, saveNoteResult, error) {
	if !validNoteKinds[args.Kind] {
		return nil, saveNoteResult{}, fmt.Errorf("kind must be one of decision, gotcha, convention, todo, fact — got %q", args.Kind)
	}
	if args.Title == "" {
		return nil, saveNoteResult{}, fmt.Errorf("title is required")
	}

	agent := "unknown"
	if ci := req.ClientInfo(); ci != nil && ci.Name != "" {
		agent = ci.Name
	}

	tags := args.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, saveNoteResult{}, err
	}

	sqlText, err := store.LoadQuery(queries.FS, "notes.sql", "SaveNote")
	if err != nil {
		return nil, saveNoteResult{}, err
	}

	// session_id is left NULL: an MCP tool call isn't necessarily tied to
	// a session ctx has already ingested (that session may still be live,
	// or belong to an agent whose adapter hasn't run yet), so there's no
	// reliable id to attach here without guessing.
	res, err := s.db.Exec(sqlText,
		sql.Named("project_id", s.projectID), sql.Named("session_id", nil), sql.Named("agent", agent),
		sql.Named("kind", args.Kind), sql.Named("title", args.Title), sql.Named("body", args.Body),
		sql.Named("tags", string(tagsJSON)))
	if err != nil {
		return nil, saveNoteResult{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, saveNoteResult{}, err
	}
	return nil, saveNoteResult{ID: id}, nil
}
