// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danPoku/kaectx/internal/store"
	"github.com/danPoku/kaectx/queries"
)

// getChunkMaxChars bounds one chunk's text. A chunk is normally ~900 tokens,
// but an unusually long assistant reply can exceed that.
const getChunkMaxChars = 12000

type getChunkArgs struct {
	ChunkID int64 `json:"chunk_id" jsonschema:"a chunk_id returned by search_context"`
}

type getChunkResult struct {
	ChunkID   int64  `json:"chunk_id"`
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
	At        string `json:"at"`
	FirstSeq  int    `json:"first_seq"`
	LastSeq   int    `json:"last_seq"`
	// Text is the exchange: "User: ..." then "Assistant: ...", conversation
	// text only, exactly what search indexed.
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated,omitempty"`
}

func (s *server) registerChunk(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_chunk",
		Description: "Read one search hit in full: the user turn and the assistant's reply it belongs to (~1k tokens), by chunk_id from search_context. " +
			"The cheap way to follow up a hit; use get_session only when you need the messages around it.",
	}, s.getChunk)
}

func (s *server) getChunk(_ context.Context, _ *mcp.CallToolRequest, args getChunkArgs) (*mcp.CallToolResult, getChunkResult, error) {
	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", "GetChunk")
	if err != nil {
		return nil, getChunkResult{}, err
	}
	var out getChunkResult
	err = s.db.QueryRow(sqlText, sql.Named("chunk_id", args.ChunkID), sql.Named("project_id", s.projectID)).
		Scan(&out.ChunkID, &out.SessionID, &out.Agent, &out.FirstSeq, &out.LastSeq, &out.Text, &out.At)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, getChunkResult{}, fmt.Errorf("no chunk %d in this project", args.ChunkID)
	}
	if err != nil {
		return nil, getChunkResult{}, err
	}
	if cut, _, ok := truncateChars(out.Text, getChunkMaxChars); ok {
		out.Text, out.TextTruncated = cut, true
	}
	return nil, out, nil
}
