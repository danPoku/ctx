package embed

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kojog/ctx/internal/store"
)

type fakeEmbedder struct {
	calls  int
	failAt int // 1-based call number to fail on; 0 = never fail
	model  string
}

func (f *fakeEmbedder) Model() string { return f.model }

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	f.calls++
	if f.failAt != 0 && f.calls == f.failAt {
		return nil, errors.New("simulated embed failure")
	}
	return fixedVector(Dims, 0.1), nil
}

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

// seedChunk inserts a minimal session (with or without a project) and one
// chunk belonging to it, returning the chunk's id.
func seedChunk(t *testing.T, db *sql.DB, sessionID string, withProject bool, text string) int64 {
	t.Helper()
	var projectID any
	if withProject {
		res, err := db.Exec(`INSERT INTO projects(name) VALUES (?)`, sessionID)
		if err != nil {
			t.Fatalf("insert project: %v", err)
		}
		id, _ := res.LastInsertId()
		projectID = id
	}
	if _, err := db.Exec(`INSERT INTO sessions(id, agent, native_id, project_id, started_at) VALUES (?,?,?,?, '2026-09-20T10:00:00Z')`,
		sessionID, "claude-code", sessionID, projectID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	res, err := db.Exec(`INSERT INTO chunks(session_id, first_seq, last_seq, text) VALUES (?,0,0,?)`, sessionID, text)
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestRunEmbedsPendingChunks(t *testing.T) {
	db := openTestDB(t)
	c1 := seedChunk(t, db, "claude-code:s1", true, "how do I open the db in WAL mode")
	c2 := seedChunk(t, db, "claude-code:s2", true, "how do I redact api keys")

	client := &fakeEmbedder{model: "nomic-embed-text"}
	n, err := Run(context.Background(), db, client, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 2 {
		t.Fatalf("processed = %d, want 2", n)
	}
	if client.calls != 2 {
		t.Errorf("Embed called %d times, want 2", client.calls)
	}

	for _, id := range []int64{c1, c2} {
		var embeddedAt, model sql.NullString
		if err := db.QueryRow(`SELECT embedded_at, embedding_model FROM chunks WHERE id = ?`, id).Scan(&embeddedAt, &model); err != nil {
			t.Fatalf("query chunk %d: %v", id, err)
		}
		if !embeddedAt.Valid || embeddedAt.String == "" {
			t.Errorf("chunk %d: embedded_at not set", id)
		}
		if model.String != "nomic-embed-text" {
			t.Errorf("chunk %d: embedding_model = %q, want nomic-embed-text", id, model.String)
		}

		var vecCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM chunk_vectors WHERE chunk_id = ?`, id).Scan(&vecCount); err != nil {
			t.Fatalf("query chunk_vectors for %d: %v", id, err)
		}
		if vecCount != 1 {
			t.Errorf("chunk %d: chunk_vectors rows = %d, want 1", id, vecCount)
		}
	}
}

func TestRunSkipsChunksWithNoProject(t *testing.T) {
	db := openTestDB(t)
	seedChunk(t, db, "claude-code:orphan", false, "a session with no resolved project")

	client := &fakeEmbedder{model: "nomic-embed-text"}
	n, err := Run(context.Background(), db, client, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 0 {
		t.Errorf("processed = %d, want 0 (no project to partition the vector on)", n)
	}
	if client.calls != 0 {
		t.Errorf("Embed called %d times, want 0 — should never even try", client.calls)
	}

	// Still pending, not silently marked embedded.
	var embeddedAt sql.NullString
	if err := db.QueryRow(`SELECT embedded_at FROM chunks`).Scan(&embeddedAt); err != nil {
		t.Fatalf("query chunk: %v", err)
	}
	if embeddedAt.Valid {
		t.Error("embedded_at is set, want it to stay NULL (still pending)")
	}
}

func TestRunRespectsBatchSize(t *testing.T) {
	db := openTestDB(t)
	seedChunk(t, db, "claude-code:s1", true, "one")
	seedChunk(t, db, "claude-code:s2", true, "two")
	seedChunk(t, db, "claude-code:s3", true, "three")

	client := &fakeEmbedder{model: "nomic-embed-text"}
	n, err := Run(context.Background(), db, client, 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 2 {
		t.Fatalf("processed = %d, want 2 (limited by batchSize)", n)
	}

	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedded_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 1 {
		t.Errorf("pending after batch = %d, want 1", pending)
	}
}

func TestRunStopsOnEmbedErrorButKeepsPriorProgress(t *testing.T) {
	db := openTestDB(t)
	seedChunk(t, db, "claude-code:s1", true, "one")
	seedChunk(t, db, "claude-code:s2", true, "two")
	seedChunk(t, db, "claude-code:s3", true, "three")

	client := &fakeEmbedder{model: "nomic-embed-text", failAt: 2}
	n, err := Run(context.Background(), db, client, 10)
	if err == nil {
		t.Fatal("Run: nil error, want the simulated failure surfaced")
	}
	if n != 1 {
		t.Fatalf("processed = %d, want 1 (the one chunk that succeeded before the failure)", n)
	}

	var embedded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedded_at IS NOT NULL`).Scan(&embedded); err != nil {
		t.Fatalf("count embedded: %v", err)
	}
	if embedded != 1 {
		t.Errorf("embedded chunks in DB = %d, want 1 (not rolled back)", embedded)
	}
}
