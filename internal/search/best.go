// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package search

import (
	"context"
	"database/sql"

	"github.com/danPoku/ctx/internal/embed"
)

// Result is search's unified hit shape, produced by either Keyword or
// Hybrid — callers that just want "the best available search" don't need
// to know which one actually ran.
type Result struct {
	ChunkID   int64
	SessionID string
	Agent     string
	StartedAt string // when the SESSION started
	At        string // when this chunk's turn happened; use this for display
	Snippet   string
	FirstSeq  int // message range of the matching chunk; pass to get_session
	LastSeq   int
}

// BestResult reports what ran, not just the hits — callers (ctx search,
// the search_context MCP tool) can tell the user semantic search quietly
// degraded instead of presenting a fallback as if it were the real thing.
type BestResult struct {
	Results    []Result
	UsedHybrid bool
	// FallbackReason is non-nil iff a client was provided but hybrid search
	// couldn't run — no embedding worker has caught up yet, vec0 isn't
	// available on this machine, or the local Ollama isn't reachable.
	FallbackReason error
}

// Best tries hybrid (keyword+semantic) search first, falling back to
// keyword-only on any failure. Semantic search is always an enhancement,
// never a requirement, matching the same "optional, log and continue"
// posture migrations/002_vectors.sql takes at the schema level: a machine
// without Ollama running, or before the embed worker has caught up, still
// gets full keyword search. client may be nil to skip straight to
// keyword-only (e.g. a caller that already knows embeddings aren't set up).
func Best(ctx context.Context, db *sql.DB, client embed.Embedder, query string, projectID int64, agent string, limit int) (BestResult, error) {
	var fallbackReason error
	if client != nil {
		hy, err := Hybrid(ctx, db, client, query, projectID, agent, limit)
		if err == nil {
			out := make([]Result, len(hy))
			for i, r := range hy {
				out[i] = Result{ChunkID: r.ChunkID, SessionID: r.SessionID, Agent: r.Agent, StartedAt: r.StartedAt, At: r.At, Snippet: r.Preview, FirstSeq: r.FirstSeq, LastSeq: r.LastSeq}
			}
			return BestResult{Results: out, UsedHybrid: true}, nil
		}
		fallbackReason = err
	}

	kw, err := Keyword(db, query, projectID, agent, limit)
	if err != nil {
		return BestResult{}, err
	}
	return BestResult{Results: toResults(kw), FallbackReason: fallbackReason}, nil
}

func toResults(kw []KeywordResult) []Result {
	out := make([]Result, len(kw))
	for i, r := range kw {
		out[i] = Result{ChunkID: r.ChunkID, SessionID: r.SessionID, Agent: r.Agent, StartedAt: r.StartedAt, At: r.At, Snippet: r.Snippet, FirstSeq: r.FirstSeq, LastSeq: r.LastSeq}
	}
	return out
}
