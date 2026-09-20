// Package mcpserver exposes ctx's retrieval and note-taking tools over MCP,
// so an agent can pull its own (or another agent's) past context mid-session
// instead of re-deriving it. Every tool response is a summary or a capped
// page — progressive disclosure — never a full unbounded transcript dump,
// so a single tool call can't swallow an agent's whole context window.
package mcpserver

import (
	"database/sql"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kojog/ctx/internal/store"
)

// server holds what every tool handler needs. projectID is resolved once,
// from the directory `ctx mcp` was launched in — the same assumption
// `ctx search` makes: a local stdio MCP server is spawned by the calling
// agent from within the project it's working on, so the server's own
// working directory IS the project.
type server struct {
	db        *sql.DB
	projectID int64
}

// New builds the ctx MCP server and registers every tool. cwd is the
// directory the `ctx mcp` process was launched from.
func New(db *sql.DB, cwd string) (*mcp.Server, error) {
	projectID, err := store.ResolveProject(db, cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve project for %s: %w", cwd, err)
	}
	s := &server{db: db, projectID: projectID}

	srv := mcp.NewServer(&mcp.Implementation{Name: "ctx", Version: "0.1.0"}, nil)
	s.registerSearch(srv)
	s.registerSessions(srv)
	s.registerNotes(srv)
	return srv, nil
}
