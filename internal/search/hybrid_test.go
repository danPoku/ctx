// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/danPoku/kaectx/internal/embed"
	"github.com/danPoku/kaectx/internal/ingest"
	"github.com/danPoku/kaectx/internal/ingest/claudecode"
	"github.com/danPoku/kaectx/internal/search"
	"github.com/danPoku/kaectx/internal/store"
)

// fixedEmbedder always returns the same vector, regardless of input text.
// Good enough to prove the plumbing (fusion, project/agent scoping, wiring
// through chunk_vectors) works — it can't prove semantic relevance, which
// needs a real model and is out of scope for a unit test.
type fixedEmbedder struct{ model string }

func (f fixedEmbedder) Model() string { return f.model }
func (f fixedEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	vec := make([]float32, embed.Dims)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func TestHybridFusesKeywordAndSemanticResults(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	result, err := store.Migrate(db)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !result.VectorsLoaded {
		t.Skip("vec0 did not load on this machine, skipping hybrid search test")
	}

	fixture, err := os.ReadFile("../ingest/claudecode/testdata/session.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, fixture, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := ingest.Run(db, claudecode.New(), sessionPath); err != nil {
		t.Fatalf("ingest.Run: %v", err)
	}

	client := fixedEmbedder{model: "test-model"}
	n, err := embed.Run(context.Background(), db, client, 10)
	if err != nil {
		t.Fatalf("embed.Run: %v", err)
	}
	if n != 1 {
		t.Fatalf("embed.Run processed = %d, want 1 (the fixture's one settled chunk)", n)
	}

	projectID, err := store.ResolveProject(db, "/home/dev/proj")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	t.Run("unscoped finds the chunk via keyword+semantic fusion", func(t *testing.T) {
		results, err := search.Hybrid(context.Background(), db, client, "WAL", projectID, "", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("results = %d, want 1", len(results))
		}
		if results[0].Agent != "claude-code" {
			t.Errorf("Agent = %q, want claude-code", results[0].Agent)
		}
		if results[0].FirstSeq != 0 || results[0].LastSeq <= results[0].FirstSeq {
			t.Errorf("range = %d-%d, want the chunk's message span starting at 0", results[0].FirstSeq, results[0].LastSeq)
		}
		if results[0].Score <= 0 {
			t.Errorf("Score = %v, want > 0", results[0].Score)
		}
	})

	t.Run("SearchHybridByAgent variant matches the right agent", func(t *testing.T) {
		results, err := search.Hybrid(context.Background(), db, client, "WAL", projectID, "claude-code", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("results = %d, want 1", len(results))
		}
	})

	t.Run("SearchHybridByAgent variant excludes a non-matching agent", func(t *testing.T) {
		results, err := search.Hybrid(context.Background(), db, client, "WAL", projectID, "codex", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("results = %+v, want 0 (this chunk belongs to claude-code)", results)
		}
	})
}
