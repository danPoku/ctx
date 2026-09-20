package main

import (
	"context"
	"fmt"
	"os"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kojog/ctx/internal/embed"
	"github.com/kojog/ctx/internal/mcpserver"
)

// runMCP serves ctx's tools over stdio. It's meant to be launched by a
// coding agent (Claude Code, Codex, ...) as a local subprocess, from within
// the project the agent is working on — mcpserver.New resolves that project
// from this process's own working directory, same as `ctx search` does.
func runMCP(args []string) error {
	db, _, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	client := embed.NewOllamaClient(embed.DefaultBaseURL, embed.DefaultModel)
	srv, err := mcpserver.New(db, cwd, client)
	if err != nil {
		return err
	}

	return srv.Run(context.Background(), &sdkmcp.StdioTransport{})
}
