package mcpserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kojog/ctx/internal/ingest"
	"github.com/kojog/ctx/internal/ingest/claudecode"
	"github.com/kojog/ctx/internal/mcpserver"
	"github.com/kojog/ctx/internal/store"
)

// testServer wires up a real ctx MCP server against a temp DB seeded with
// the claudecode fixture (cwd "/home/dev/proj"), connected in-process to a
// real MCP client — no subprocess, but a genuine client/server MCP
// round-trip (initialize handshake, JSON schema validation, wire encoding)
// rather than calling handler functions directly.
func testServer(t *testing.T) (*sdkmcp.ClientSession, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fixture, err := os.ReadFile("../ingest/claudecode/testdata/session.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sessionPath, fixture, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := ingest.Run(db, claudecode.New(), sessionPath); err != nil {
		t.Fatalf("ingest.Run: %v", err)
	}

	srv, err := mcpserver.New(db, "/home/dev/proj")
	if err != nil {
		t.Fatalf("mcpserver.New: %v", err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	return clientSession, db
}

// callTool calls name with args and decodes the tool's auto-populated JSON
// text content into out — the same wire path a real MCP client goes
// through, not a shortcut to the handler function.
func callTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned a tool error: %+v", name, res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(%s): no content in result", name)
	}
	tc, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): content[0] is %T, want *TextContent", name, res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
		t.Fatalf("CallTool(%s): decode result: %v\nraw: %s", name, err, tc.Text)
	}
}

func TestSearchContext(t *testing.T) {
	cs, _ := testServer(t)

	var out struct {
		Results []struct {
			ChunkID   int64  `json:"chunk_id"`
			SessionID string `json:"session_id"`
			Agent     string `json:"agent"`
			Snippet   string `json:"snippet"`
		} `json:"results"`
	}
	callTool(t, cs, "search_context", map[string]any{"query": "WAL"}, &out)

	if len(out.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(out.Results))
	}
	if out.Results[0].Agent != "claude-code" {
		t.Errorf("Agent = %q, want claude-code", out.Results[0].Agent)
	}
	if !strings.Contains(out.Results[0].Snippet, "WAL") {
		t.Errorf("Snippet = %q, want it to contain WAL", out.Results[0].Snippet)
	}
}

func TestRecentSessions(t *testing.T) {
	cs, _ := testServer(t)

	var out struct {
		Sessions []struct {
			ID    string `json:"id"`
			Agent string `json:"agent"`
			Title string `json:"title"`
		} `json:"sessions"`
	}
	callTool(t, cs, "recent_sessions", map[string]any{}, &out)

	if len(out.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(out.Sessions))
	}
	if out.Sessions[0].ID != "claude-code:sess-1" {
		t.Errorf("ID = %q, want claude-code:sess-1", out.Sessions[0].ID)
	}
	if out.Sessions[0].Title != "WAL mode question" {
		t.Errorf("Title = %q, want the ai-title event's value", out.Sessions[0].Title)
	}
}

func TestGetSessionPagesAndCaps(t *testing.T) {
	cs, _ := testServer(t)

	var out struct {
		Messages []struct {
			Seq     int    `json:"seq"`
			Role    string `json:"role"`
			Kind    string `json:"kind"`
			Content string `json:"content"`
		} `json:"messages"`
		Truncated   bool `json:"truncated"`
		NextFromSeq *int `json:"next_from_seq"`
	}
	callTool(t, cs, "get_session", map[string]any{"session_id": "claude-code:sess-1"}, &out)

	if len(out.Messages) == 0 {
		t.Fatal("messages is empty, want the fixture's messages")
	}
	for _, m := range out.Messages {
		if m.Kind == "thinking" {
			t.Errorf("message seq=%d kind=thinking leaked through — GetSessionMessages excludes reasoning traces", m.Seq)
		}
	}

	// The fixture has 8 messages total (well under the 200-row cap), so a
	// default call must NOT report truncation.
	if out.Truncated {
		t.Errorf("Truncated = true for a small session, want false")
	}
}

func TestSessionsTouching(t *testing.T) {
	cs, db := testServer(t)

	// The claudecode fixture's tool calls don't touch a file with the
	// "file_path" shape file_touches extraction looks for (it uses Read
	// with a "path" key and Bash), so seed a file_touches row directly to
	// test the tool's own query/response shape rather than re-testing
	// extraction (already covered in internal/ingest's own tests).
	if _, err := db.Exec(`INSERT INTO file_touches(session_id, project_id, path, action, created_at)
	                       VALUES ('claude-code:sess-1', (SELECT project_id FROM sessions WHERE id='claude-code:sess-1'), 'main.go', 'edit', '2026-09-20T10:00:00Z')`); err != nil {
		t.Fatalf("seed file_touches: %v", err)
	}

	var out struct {
		Sessions []struct {
			SessionID string   `json:"session_id"`
			Agent     string   `json:"agent"`
			Actions   []string `json:"actions"`
			Touches   int      `json:"touches"`
		} `json:"sessions"`
	}
	callTool(t, cs, "sessions_touching", map[string]any{"path": "main.go"}, &out)

	if len(out.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(out.Sessions))
	}
	if out.Sessions[0].SessionID != "claude-code:sess-1" {
		t.Errorf("SessionID = %q, want claude-code:sess-1", out.Sessions[0].SessionID)
	}
	if out.Sessions[0].Touches != 1 {
		t.Errorf("Touches = %d, want 1", out.Sessions[0].Touches)
	}
}

func TestSaveNoteAndSearchNotes(t *testing.T) {
	cs, _ := testServer(t)

	var saveOut struct {
		ID int64 `json:"id"`
	}
	callTool(t, cs, "save_note", map[string]any{
		"kind": "decision", "title": "Use WAL mode", "body": "WAL avoids writer/reader blocking.",
		"tags": []string{"sqlite"},
	}, &saveOut)
	if saveOut.ID == 0 {
		t.Fatal("save_note returned id=0, want a real row id")
	}

	var searchOut struct {
		Notes []struct {
			ID    int64    `json:"id"`
			Kind  string   `json:"kind"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
			Agent string   `json:"agent"`
		} `json:"notes"`
	}
	callTool(t, cs, "search_notes", map[string]any{"query": "WAL"}, &searchOut)

	if len(searchOut.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(searchOut.Notes))
	}
	n := searchOut.Notes[0]
	if n.ID != saveOut.ID {
		t.Errorf("ID = %d, want %d", n.ID, saveOut.ID)
	}
	if n.Kind != "decision" || n.Title != "Use WAL mode" {
		t.Errorf("note = %+v, want kind=decision title=%q", n, "Use WAL mode")
	}
	if len(n.Tags) != 1 || n.Tags[0] != "sqlite" {
		t.Errorf("Tags = %v, want [sqlite]", n.Tags)
	}
	// The test client identifies itself as "test-client" during the MCP
	// handshake — proof save_note actually reads the caller's ClientInfo
	// rather than hardcoding an agent name.
	if n.Agent != "test-client" {
		t.Errorf("Agent = %q, want test-client (from the MCP client's own Implementation.Name)", n.Agent)
	}
}

func TestSaveNoteRejectsInvalidKind(t *testing.T) {
	cs, _ := testServer(t)

	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "save_note",
		Arguments: map[string]any{"kind": "not-a-real-kind", "title": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Error("save_note with an invalid kind: IsError = false, want true")
	}
}
