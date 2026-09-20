package ingest_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kojog/ctx/internal/ingest"
	"github.com/kojog/ctx/internal/ingest/claudecode"
	"github.com/kojog/ctx/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// copyFixture puts a mutable copy of the claudecode adapter's fixture
// session into a temp file, so tests can append to it to simulate a session
// continuing between two ingest runs without touching the checked-in
// testdata.
func copyFixture(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("claudecode/testdata/session.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, src, 0644); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}
	return path
}

func TestRunIngestsSessionAndChunksOnlySettledTurns(t *testing.T) {
	db := openTestDB(t)
	path := copyFixture(t)
	adapter := claudecode.New()

	if err := ingest.Run(db, adapter, path); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var msgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 8 {
		t.Errorf("messages = %d, want 8", msgCount)
	}

	var sessionCount int
	var title, agent string
	if err := db.QueryRow(`SELECT COUNT(*), title, agent FROM sessions GROUP BY id`).Scan(&sessionCount, &title, &agent); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if sessionCount != 1 {
		t.Errorf("sessions = %d, want 1", sessionCount)
	}
	if title != "WAL mode question" {
		t.Errorf("title = %q, want the ai-title event's value", title)
	}
	if agent != "claude-code" {
		t.Errorf("agent = %q, want claude-code", agent)
	}

	// The fixture's second turn ends on a tool_result with no closing
	// assistant text yet — it must NOT be chunked (the "settled tail"
	// rule), so only the first, complete turn should be indexed.
	var chunkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkCount); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if chunkCount != 1 {
		t.Errorf("chunks = %d, want 1 (second turn is still dangling)", chunkCount)
	}

	var firstSeq, lastSeq int
	var text string
	if err := db.QueryRow(`SELECT first_seq, last_seq, text FROM chunks`).Scan(&firstSeq, &lastSeq, &text); err != nil {
		t.Fatalf("query chunk: %v", err)
	}
	if firstSeq != 0 || lastSeq != 4 {
		t.Errorf("chunk span = [%d,%d], want [0,4]", firstSeq, lastSeq)
	}
	if !strings.Contains(text,"WAL mode") || !strings.Contains(text,"_journal_mode=WAL") {
		t.Errorf("chunk text = %q, want it to contain both the question and the answer", text)
	}
	// Tool noise must be trimmed out of the indexed text.
	if strings.Contains(text,"toolu_1") || strings.Contains(text,"Read") {
		t.Errorf("chunk text = %q, tool call/result leaked into the index", text)
	}
}

func TestRunIsIdempotentAndCompletesDanglingTurnsOnReingest(t *testing.T) {
	db := openTestDB(t)
	path := copyFixture(t)
	adapter := claudecode.New()

	if err := ingest.Run(db, adapter, path); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Re-running with no new bytes must be a pure no-op.
	if err := ingest.Run(db, adapter, path); err != nil {
		t.Fatalf("second Run (no new data): %v", err)
	}
	var msgCount, chunkCount int
	db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkCount)
	if msgCount != 8 {
		t.Errorf("after no-op re-run: messages = %d, want 8 (no duplicates)", msgCount)
	}
	if chunkCount != 1 {
		t.Errorf("after no-op re-run: chunks = %d, want 1 (no duplicates)", chunkCount)
	}

	// Now the assistant finishes the second (previously dangling) turn.
	closing := `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"Use internal/redact.Redact on both content and raw before insert."}]},"sessionId":"sess-1","cwd":"/home/dev/proj","timestamp":"2026-09-20T10:01:03Z"}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open fixture for append: %v", err)
	}
	if _, err := f.WriteString(closing); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	if err := ingest.Run(db, adapter, path); err != nil {
		t.Fatalf("third Run (new data): %v", err)
	}
	db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkCount)
	if msgCount != 9 {
		t.Errorf("after completing turn: messages = %d, want 9", msgCount)
	}
	if chunkCount != 2 {
		t.Errorf("after completing turn: chunks = %d, want 2 (dangling turn now settled)", chunkCount)
	}

	var firstSeq, lastSeq int
	if err := db.QueryRow(`SELECT first_seq, last_seq FROM chunks ORDER BY first_seq DESC LIMIT 1`).Scan(&firstSeq, &lastSeq); err != nil {
		t.Fatalf("query newest chunk: %v", err)
	}
	if firstSeq != 5 || lastSeq != 8 {
		t.Errorf("newest chunk span = [%d,%d], want [5,8]", firstSeq, lastSeq)
	}
}

func TestRunLogsMalformedLinesWithoutAbortingTheFile(t *testing.T) {
	db := openTestDB(t)
	adapter := claudecode.New()

	content := `{"type":"user","message":{"role":"user","content":"first"},"sessionId":"sess-2","cwd":"/x","timestamp":"2026-09-20T10:00:00Z"}
not valid json at all
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}]},"sessionId":"sess-2","timestamp":"2026-09-20T10:00:01Z"}
`
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := ingest.Run(db, adapter, path); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var msgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 2 {
		t.Errorf("messages = %d, want 2 (the malformed line skipped, its neighbors kept)", msgCount)
	}

	var lastErr sql.NullString
	if err := db.QueryRow(`SELECT last_error FROM sources WHERE path = ?`, path).Scan(&lastErr); err != nil {
		t.Fatalf("query source: %v", err)
	}
	if !lastErr.Valid || lastErr.String == "" {
		t.Errorf("sources.last_error = %v, want the malformed line's error recorded", lastErr)
	}
}
