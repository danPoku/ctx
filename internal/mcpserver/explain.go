// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danPoku/kaectx/internal/explain"
)

type explainFileArgs struct {
	Path  string `json:"path" jsonschema:"project-relative file path, e.g. internal/store/store.go"`
	Limit int    `json:"limit,omitempty" jsonschema:"max sessions, default 10"`
}

func (s *server) registerExplain(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "explain_file",
		Description: "Build a compact briefing for a file before editing it: sessions that touched it, actions, known git commit span, and chunk ids to open for the reasoning behind the work.",
	}, s.explainFile)
}

func (s *server) explainFile(_ context.Context, _ *mcp.CallToolRequest, args explainFileArgs) (*mcp.CallToolResult, explain.Result, error) {
	out, err := explain.File(s.db, s.projectID, explain.ProjectPath(s.cwd, args.Path), args.Limit)
	if err != nil {
		return nil, explain.Result{}, err
	}
	return nil, out, nil
}
