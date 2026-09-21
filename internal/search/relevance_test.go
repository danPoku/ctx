// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/danPoku/kaectx/internal/embed"
	"github.com/danPoku/kaectx/internal/search"
	"github.com/danPoku/kaectx/internal/store"
)

// axisEmbedder embeds every query as the unit vector along dimension 0, so a
// stored vector's cosine distance to the query is fully controlled by the
// test: distance = 1 - cos(angle).
type axisEmbedder struct{}

func (axisEmbedder) Model() string { return "axis" }
func (axisEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, embed.Dims)
	v[0] = 1
	return v, nil
}

// vecAtDistance returns a unit vector whose cosine distance from axisEmbedder's
// query is d (0 <= d <= 1): cos = 1-d, sin = sqrt(1-cos^2) on dimension 1.
func vecAtDistance(d float64) []float32 {
	cos := 1 - d
	v := make([]float32, embed.Dims)
	v[0] = float32(cos)
	v[1] = float32(math.Sqrt(1 - cos*cos))
	return v
}

func newVecDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := store.Migrate(db)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !res.VectorsLoaded {
		t.Skip("vec0 did not load on this machine")
	}
	return db
}

// seedChunk inserts a session (own project unless projectID != 0) with one
// chunk and, when vec != nil, its embedding. Returns the chunk id.
func seedChunkWithVector(t *testing.T, db *sql.DB, projectID int64, agent, session, startedAt, text string, vec []float32) int64 {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO sessions(id, agent, native_id, project_id, started_at) VALUES (?,?,?,?,?)`,
		session, agent, session, projectID, startedAt); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	res, err := db.Exec(`INSERT INTO chunks(session_id, first_seq, last_seq, text) VALUES (?,0,1,?)`, session, text)
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	id, _ := res.LastInsertId()
	if vec != nil {
		j, _ := json.Marshal(vec)
		if _, err := db.Exec(`INSERT INTO chunk_vectors(chunk_id, project_id, agent, embedding, session_id) VALUES (?,?,?,?,?)`,
			id, projectID, agent, string(j), session); err != nil {
			t.Fatalf("insert vector: %v", err)
		}
	}
	return id
}

func newProject(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO projects(name) VALUES ('p')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestHybridDropsSemanticNeighboursBeyondTheDistanceFloor(t *testing.T) {
	db := newVecDB(t)
	p := newProject(t, db)

	// Text never matches the query words, so only the vector side can return them.
	near := seedChunkWithVector(t, db, p, "claude-code", "s-near", "2026-09-20T10:00:00Z", "alpha", vecAtDistance(0.05))
	onTopic := seedChunkWithVector(t, db, p, "codex", "s-topic", "2026-09-20T10:01:00Z", "beta", vecAtDistance(search.MaxSemanticDistance-0.05))
	far := seedChunkWithVector(t, db, p, "claude-code", "s-far", "2026-09-20T10:02:00Z", "gamma", vecAtDistance(search.MaxSemanticDistance+0.05))
	opposite := seedChunkWithVector(t, db, p, "codex", "s-opp", "2026-09-20T10:03:00Z", "delta", vecAtDistance(1.0))

	ids := func(rs []search.HybridResult) map[int64]bool {
		m := map[int64]bool{}
		for _, r := range rs {
			m[r.ChunkID] = true
		}
		return m
	}

	t.Run("cross-agent", func(t *testing.T) {
		rs, err := search.Hybrid(context.Background(), db, axisEmbedder{}, "zzznomatch", p, "", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		got := ids(rs)
		if !got[near] || !got[onTopic] {
			t.Errorf("relevant neighbours missing: got %v", got)
		}
		if got[far] || got[opposite] {
			t.Errorf("irrelevant neighbours leaked in: got %v", got)
		}
	})

	t.Run("agent-scoped variant applies the same floor", func(t *testing.T) {
		rs, err := search.Hybrid(context.Background(), db, axisEmbedder{}, "zzznomatch", p, "claude-code", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		got := ids(rs)
		if !got[near] {
			t.Errorf("near claude-code chunk missing: %v", got)
		}
		if got[far] {
			t.Errorf("far claude-code chunk leaked in: %v", got)
		}
	})

	t.Run("nothing close enough gives no results, not padding", func(t *testing.T) {
		p2 := newProject(t, db)
		seedChunkWithVector(t, db, p2, "codex", "s-only-far", "2026-09-20T10:04:00Z", "epsilon", vecAtDistance(0.9))
		rs, err := search.Hybrid(context.Background(), db, axisEmbedder{}, "zzznomatch", p2, "", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(rs) != 0 {
			t.Errorf("results = %+v, want none", rs)
		}
	})

	t.Run("a keyword hit is kept even when its vector is far", func(t *testing.T) {
		p3 := newProject(t, db)
		kw := seedChunkWithVector(t, db, p3, "codex", "s-kw", "2026-09-20T10:05:00Z", "ZEPHYRQUOKKA exact identifier", vecAtDistance(0.9))
		rs, err := search.Hybrid(context.Background(), db, axisEmbedder{}, "ZEPHYRQUOKKA", p3, "", 10)
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(rs) != 1 || rs[0].ChunkID != kw {
			t.Errorf("results = %+v, want only the keyword hit", rs)
		}
	})
}

func TestResultsCarryTheTurnTimeNotTheSessionStart(t *testing.T) {
	db := newVecDB(t)
	p := newProject(t, db)

	const sessionStart = "2026-09-20T09:00:00Z"
	id := seedChunkWithVector(t, db, p, "claude-code", "s-long", sessionStart, "banana turn", vecAtDistance(0.05))
	// Two messages inside the chunk's span; the turn began at the earlier one.
	for _, m := range []struct {
		seq int
		at  any
	}{{0, "2026-09-20T15:30:00Z"}, {1, "2026-09-20T15:30:09Z"}} {
		if _, err := db.Exec(`INSERT INTO messages(session_id, seq, role, kind, content, created_at) VALUES ('s-long', ?, 'user', 'text', 'x', ?)`, m.seq, m.at); err != nil {
			t.Fatal(err)
		}
	}
	_ = id

	// A second session whose messages carry no timestamps falls back to its start.
	seedChunkWithVector(t, db, p, "codex", "s-notime", "2026-09-20T11:11:11Z", "banana untimed", vecAtDistance(0.05))
	if _, err := db.Exec(`INSERT INTO messages(session_id, seq, role, kind, content, created_at) VALUES ('s-notime', 0, 'user', 'text', 'x', NULL)`); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"s-long": "2026-09-20T15:30:00Z", "s-notime": "2026-09-20T11:11:11Z"}

	check := func(name string, got map[string]string) {
		for sess, at := range want {
			if got[sess] != at {
				t.Errorf("%s: %s At = %q, want %q", name, sess, got[sess], at)
			}
		}
	}

	kw, err := search.Keyword(db, "banana", p, "", 10)
	if err != nil {
		t.Fatalf("Keyword: %v", err)
	}
	gotKW := map[string]string{}
	for _, r := range kw {
		gotKW[r.SessionID] = r.At
	}
	check("Keyword", gotKW)

	hy, err := search.Hybrid(context.Background(), db, axisEmbedder{}, "banana", p, "", 10)
	if err != nil {
		t.Fatalf("Hybrid: %v", err)
	}
	gotHY := map[string]string{}
	for _, r := range hy {
		gotHY[r.SessionID] = r.At
	}
	check("Hybrid", gotHY)

	// And the unified shape callers use.
	best, err := search.Best(context.Background(), db, axisEmbedder{}, "banana", p, "", 10)
	if err != nil {
		t.Fatalf("Best: %v", err)
	}
	gotBest := map[string]string{}
	for _, r := range best.Results {
		gotBest[r.SessionID] = r.At
		if r.SessionID == "s-long" && r.StartedAt != sessionStart {
			t.Errorf("StartedAt = %q, want the session start %q to remain available", r.StartedAt, sessionStart)
		}
	}
	check("Best", gotBest)
}
