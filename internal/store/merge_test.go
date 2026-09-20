// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"testing"
)

// seedProject inserts a project (remote may be "") with one path, one
// session and one already-embedded chunk, and returns the project id.
func seedProject(t *testing.T, db *sql.DB, name, remote, path, sessionID string) int64 {
	t.Helper()
	var rem any
	if remote != "" {
		rem = remote
	}
	res, err := db.Exec(`INSERT INTO projects(name, git_remote) VALUES (?, ?)`, name, rem)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO project_paths(path, project_id) VALUES (?, ?)`, path, id); err != nil {
		t.Fatalf("insert path: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id, agent, native_id, project_id, started_at) VALUES (?, 'claude-code', ?, ?, '2026-01-01T00:00:00Z')`,
		sessionID, sessionID, id); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO chunks(session_id, first_seq, last_seq, text, embedded_at, embedding_model)
		VALUES (?, 0, 1, 'hello', '2026-01-01T00:00:00Z', 'nomic-embed-text')`, sessionID); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	return id
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestMergeProjectsMovesEverythingAndInheritsRemote(t *testing.T) {
	db := migratedTestDB(t)
	// `from` is the orphan; `into` has no remote yet, `from` does: into must inherit it.
	from := seedProject(t, db, "ctx", "github.com/dan/ctx", "/old/ctx", "s-old")
	into := seedProject(t, db, "ctx", "", "/new/ctx", "s-new")

	if err := MergeProjects(db, from, into); err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM projects WHERE id = ?`, from); n != 0 {
		t.Errorf("source project still exists")
	}
	if n := count(t, db, `SELECT count(*) FROM sessions WHERE project_id = ?`, into); n != 2 {
		t.Errorf("sessions in target = %d, want 2", n)
	}
	if n := count(t, db, `SELECT count(*) FROM project_paths WHERE project_id = ?`, into); n != 2 {
		t.Errorf("paths in target = %d, want 2", n)
	}
	if n := count(t, db, `SELECT count(*) FROM projects WHERE id = ? AND git_remote = 'github.com/dan/ctx'`, into); n != 1 {
		t.Errorf("target did not inherit the remote")
	}
	// The moved session's chunk must be pending again; the untouched one stays embedded.
	if n := count(t, db, `SELECT count(*) FROM chunks WHERE session_id = 's-old' AND embedded_at IS NULL`); n != 1 {
		t.Errorf("moved chunk not requeued for embedding")
	}
	if n := count(t, db, `SELECT count(*) FROM chunks WHERE session_id = 's-new' AND embedded_at IS NOT NULL`); n != 1 {
		t.Errorf("target's own chunk was requeued unexpectedly")
	}
}

func TestMergeProjectsRefusals(t *testing.T) {
	db := migratedTestDB(t)
	a := seedProject(t, db, "a", "github.com/dan/a", "/a", "s-a")
	b := seedProject(t, db, "b", "github.com/dan/b", "/b", "s-b")

	cases := []struct {
		name       string
		from, into int64
	}{
		{"same project", a, a},
		{"both have remotes", a, b},
		{"missing source", 9999, a},
		{"missing target", a, 9999},
	}
	for _, tc := range cases {
		if err := MergeProjects(db, tc.from, tc.into); err == nil {
			t.Errorf("%s: expected an error, got nil", tc.name)
		}
	}
	// A refused merge must leave both projects intact.
	if n := count(t, db, `SELECT count(*) FROM projects`); n != 2 {
		t.Errorf("projects = %d after refused merges, want 2", n)
	}
}
