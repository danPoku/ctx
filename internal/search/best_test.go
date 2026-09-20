package search_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kojog/ctx/internal/ingest"
	"github.com/kojog/ctx/internal/ingest/claudecode"
	"github.com/kojog/ctx/internal/search"
	"github.com/kojog/ctx/internal/store"
)

// brokenEmbedder always fails — simulates Ollama not being reachable, or
// not having the model pulled.
type brokenEmbedder struct{}

func (brokenEmbedder) Model() string { return "broken" }
func (brokenEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("simulated: connection refused")
}

func TestBestFallsBackToKeywordWhenEmbeddingFails(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
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

	projectID, err := store.ResolveProject(db, "/home/dev/proj")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	got, err := search.Best(context.Background(), db, brokenEmbedder{}, "WAL", projectID, "", 10)
	if err != nil {
		t.Fatalf("Best: %v", err)
	}
	if got.UsedHybrid {
		t.Error("UsedHybrid = true, want false (the embedder always fails)")
	}
	if got.FallbackReason == nil {
		t.Error("FallbackReason = nil, want the embed error surfaced")
	}
	if len(got.Results) != 1 {
		t.Fatalf("Results = %d, want 1 (keyword search should still work)", len(got.Results))
	}
	if got.Results[0].Agent != "claude-code" {
		t.Errorf("Agent = %q, want claude-code", got.Results[0].Agent)
	}
}

func TestBestSkipsHybridWithNilClient(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
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

	projectID, err := store.ResolveProject(db, "/home/dev/proj")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	got, err := search.Best(context.Background(), db, nil, "WAL", projectID, "", 10)
	if err != nil {
		t.Fatalf("Best: %v", err)
	}
	if got.UsedHybrid {
		t.Error("UsedHybrid = true, want false (nil client)")
	}
	if got.FallbackReason != nil {
		t.Errorf("FallbackReason = %v, want nil (a nil client isn't a failure)", got.FallbackReason)
	}
	if len(got.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(got.Results))
	}
}
