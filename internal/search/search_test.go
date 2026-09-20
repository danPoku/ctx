package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danPoku/ctx/internal/ingest"
	"github.com/danPoku/ctx/internal/ingest/claudecode"
	"github.com/danPoku/ctx/internal/ingest/codex"
	"github.com/danPoku/ctx/internal/search"
	"github.com/danPoku/ctx/internal/store"
)

// TestKeywordFindsIngestedChunk is the end-to-end proof for milestone 2:
// ingest a real-shaped session, then find it by a phrase that only appears
// split across the user's question and the assistant's reply — the exact
// thing a chunk (not a single message) makes searchable.
func TestKeywordFindsIngestedChunk(t *testing.T) {
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

	results, err := search.Keyword(db, "WAL", projectID, "", 10)
	if err != nil {
		t.Fatalf("Keyword: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Keyword(\"WAL\") returned %d results, want 1", len(results))
	}
	if results[0].Agent != "claude-code" {
		t.Errorf("Agent = %q, want claude-code", results[0].Agent)
	}
	if !strings.Contains(results[0].Snippet, "WAL") {
		t.Errorf("Snippet = %q, want it to contain the matched term", results[0].Snippet)
	}

	// A different project must see nothing, even though the chunk exists.
	otherProjectID, err := store.ResolveProject(db, "/home/dev/unrelated")
	if err != nil {
		t.Fatalf("ResolveProject (other): %v", err)
	}
	empty, err := search.Keyword(db, "WAL", otherProjectID, "", 10)
	if err != nil {
		t.Fatalf("Keyword (other project): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Keyword scoped to an unrelated project returned %d results, want 0", len(empty))
	}

	// An agent filter that doesn't match the ingested session finds nothing.
	none, err := search.Keyword(db, "WAL", projectID, "codex", 10)
	if err != nil {
		t.Fatalf("Keyword (agent filter): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Keyword(agent=codex) returned %d results, want 0", len(none))
	}
}

// TestKeywordCrossAgent is milestone 3's other deliverable: two different
// agents' sessions in the SAME project must both be reachable through one
// search, unfiltered, and the :agent param must correctly narrow to just
// one when asked.
func TestKeywordCrossAgent(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ingestFixture := func(t *testing.T, src, dst string, adapter ingest.Adapter) {
		t.Helper()
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", src, err)
		}
		if err := os.WriteFile(dst, b, 0644); err != nil {
			t.Fatalf("write fixture copy %s: %v", dst, err)
		}
		if err := ingest.Run(db, adapter, dst); err != nil {
			t.Fatalf("ingest.Run(%s): %v", dst, err)
		}
	}

	// Both fixtures use cwd "/home/dev/proj" — same project, two agents.
	ingestFixture(t, "../ingest/claudecode/testdata/session.jsonl", filepath.Join(dir, "claude.jsonl"), claudecode.New())
	ingestFixture(t, "../ingest/codex/testdata/session.jsonl", filepath.Join(dir, "codex.jsonl"), codex.New())

	projectID, err := store.ResolveProject(db, "/home/dev/proj")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	// "WAL" only appears in the Claude Code fixture's completed turn.
	walResults, err := search.Keyword(db, "WAL", projectID, "", 10)
	if err != nil {
		t.Fatalf("Keyword(WAL): %v", err)
	}
	if len(walResults) != 1 || walResults[0].Agent != "claude-code" {
		t.Errorf("Keyword(WAL, agent=any) = %+v, want exactly one claude-code result", walResults)
	}

	// "files" only appears in the Codex fixture's completed turn.
	filesResults, err := search.Keyword(db, "files", projectID, "", 10)
	if err != nil {
		t.Fatalf("Keyword(files): %v", err)
	}
	if len(filesResults) != 1 || filesResults[0].Agent != "codex" {
		t.Errorf("Keyword(files, agent=any) = %+v, want exactly one codex result", filesResults)
	}

	// Scoping the Codex-only term to agent=claude-code must find nothing —
	// proof the :agent filter, not coincidence, is what separated them above.
	crossFiltered, err := search.Keyword(db, "files", projectID, "claude-code", 10)
	if err != nil {
		t.Fatalf("Keyword(files, agent=claude-code): %v", err)
	}
	if len(crossFiltered) != 0 {
		t.Errorf("Keyword(files, agent=claude-code) = %+v, want 0 (that turn belongs to codex)", crossFiltered)
	}
}
