// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danPoku/kaectx/internal/store"
)

func testDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	res, err := db.Exec(`INSERT INTO projects(name) VALUES ('p')`)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	id, _ := res.LastInsertId()
	return db, id
}

func exec1(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// seedSession makes a session with nMsgs messages and 2-message chunks
// ([0-1], [2-3], ...) and returns the chunk ids in order.
func seedSession(t *testing.T, db *sql.DB, project int64, id string, nMsgs int) []int64 {
	t.Helper()
	exec1(t, db, `INSERT INTO sessions(id, agent, native_id, project_id, started_at, title) VALUES (?, 'codex', ?, ?, '2026-01-01T00:00:00Z', ?)`,
		id, id, project, "title "+id)
	for i := 0; i < nMsgs; i++ {
		exec1(t, db, `INSERT INTO messages(session_id, seq, role, kind, content) VALUES (?, ?, 'assistant', 'text', 'm')`, id, i)
	}
	var chunks []int64
	for first := 0; first+1 < nMsgs; first += 2 {
		res, err := db.Exec(`INSERT INTO chunks(session_id, first_seq, last_seq, text) VALUES (?,?,?,?)`,
			id, first, first+1, fmt.Sprintf("chunk %d-%d of %s", first, first+1, id))
		if err != nil {
			t.Fatalf("chunk: %v", err)
		}
		cid, _ := res.LastInsertId()
		chunks = append(chunks, cid)
	}
	return chunks
}

func touch(t *testing.T, db *sql.DB, project int64, session string, seq int, path, action, at string) {
	t.Helper()
	exec1(t, db, `INSERT INTO file_touches(session_id, message_id, project_id, path, action, created_at)
	              VALUES (?, (SELECT id FROM messages WHERE session_id=? AND seq=?), ?, ?, ?, ?)`,
		session, session, seq, project, path, action, at)
}

// A session that only read the file, more recently, must not outrank the one
// that actually edited it.
func TestFileRanksChangesBeforeReads(t *testing.T) {
	db, p := testDB(t)
	seedSession(t, db, p, "reader", 4)
	seedSession(t, db, p, "editor", 4)
	touch(t, db, p, "editor", 1, "a.go", "edit", "2026-01-01T10:00:00Z")
	touch(t, db, p, "reader", 1, "a.go", "read", "2026-02-01T10:00:00Z")

	out, err := File(db, p, "a.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sessions) != 2 || out.Sessions[0].SessionID != "editor" || out.Sessions[1].SessionID != "reader" {
		t.Fatalf("order = %+v, want editor then reader", out.Sessions)
	}
	if out.Sessions[0].Changes != 1 || out.Sessions[1].Changes != 0 {
		t.Errorf("changes = %d/%d, want 1/0", out.Sessions[0].Changes, out.Sessions[1].Changes)
	}
}

// Within the same class, newest first; ties broken by id so the order is
// reproducible.
func TestFileOrdersNewestFirstWithStableTies(t *testing.T) {
	db, p := testDB(t)
	for _, id := range []string{"s-b", "s-a", "s-old"} {
		seedSession(t, db, p, id, 2)
	}
	touch(t, db, p, "s-b", 0, "a.go", "edit", "2026-05-01T00:00:00Z")
	touch(t, db, p, "s-a", 0, "a.go", "edit", "2026-05-01T00:00:00Z")
	touch(t, db, p, "s-old", 0, "a.go", "edit", "2026-01-01T00:00:00Z")

	out, err := File(db, p, "a.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range out.Sessions {
		got = append(got, s.SessionID)
	}
	if strings.Join(got, ",") != "s-a,s-b,s-old" {
		t.Errorf("order = %v, want s-a,s-b,s-old", got)
	}
}

// The chunk and preview must come from where the file was touched, not from
// the session's opening prompt.
func TestFileAnchorsChunkToTheTouch(t *testing.T) {
	db, p := testDB(t)
	chunks := seedSession(t, db, p, "s", 6) // chunks [0-1] [2-3] [4-5]
	// seq 2 is the FIRST message of its chunk, so "nearest earlier chunk"
	// would wrongly pick [0-1]; only true containment finds [2-3].
	touch(t, db, p, "s", 2, "a.go", "edit", "2026-01-01T10:00:00Z")

	out, err := File(db, p, "a.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	s := out.Sessions[0]
	if s.ChunkID == nil || *s.ChunkID != chunks[1] {
		t.Fatalf("chunk = %v, want the [2-3] chunk %d, not the first (%d)", s.ChunkID, chunks[1], chunks[0])
	}
	if !strings.Contains(s.Preview, "chunk 2-3") {
		t.Errorf("preview = %q", s.Preview)
	}
}

// A change beats a read when choosing the anchor, even if the read is later.
func TestFileAnchorPrefersTheLatestChangeOverALaterRead(t *testing.T) {
	db, p := testDB(t)
	chunks := seedSession(t, db, p, "s", 6)
	touch(t, db, p, "s", 1, "a.go", "edit", "2026-01-01T10:00:00Z")
	touch(t, db, p, "s", 5, "a.go", "read", "2026-01-01T11:00:00Z")

	out, _ := File(db, p, "a.go", 10)
	if id := out.Sessions[0].ChunkID; id == nil || *id != chunks[0] {
		t.Errorf("chunk = %v, want the edit's chunk %d", id, chunks[0])
	}
}

// A touch in an unsettled tail turn has no chunk yet: use the nearest earlier
// one, and with no chunks at all leave chunk_id empty rather than failing.
func TestFileAnchorFallbacks(t *testing.T) {
	db, p := testDB(t)
	chunks := seedSession(t, db, p, "tail", 6)
	exec1(t, db, `INSERT INTO messages(session_id, seq, role, kind, content) VALUES ('tail', 9, 'assistant', 'text', 'x')`)
	touch(t, db, p, "tail", 9, "a.go", "edit", "2026-01-01T10:00:00Z")

	seedSession(t, db, p, "nochunks", 1)
	touch(t, db, p, "nochunks", 0, "a.go", "edit", "2026-01-01T09:00:00Z")

	out, err := File(db, p, "a.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Session{}
	for _, s := range out.Sessions {
		byID[s.SessionID] = s
	}
	if id := byID["tail"].ChunkID; id == nil || *id != chunks[2] {
		t.Errorf("tail chunk = %v, want nearest earlier chunk %d", id, chunks[2])
	}
	if byID["nochunks"].ChunkID != nil || byID["nochunks"].Preview != "" {
		t.Errorf("nochunks = %+v, want no chunk/preview", byID["nochunks"])
	}
}

func TestFileCapsTheLimit(t *testing.T) {
	db, p := testDB(t)
	for i := 0; i < MaxLimit+10; i++ {
		id := fmt.Sprintf("s%03d", i)
		seedSession(t, db, p, id, 2)
		touch(t, db, p, id, 0, "a.go", "edit", "2026-01-01T00:00:00Z")
	}
	for _, limit := range []int{100000, 0, -5} {
		out, err := File(db, p, "a.go", limit)
		if err != nil {
			t.Fatal(err)
		}
		want := MaxLimit
		if limit <= 0 {
			want = DefaultLimit
		}
		if len(out.Sessions) != want {
			t.Errorf("limit %d returned %d sessions, want %d", limit, len(out.Sessions), want)
		}
	}
}

// No sessions must serialise as [] and no actions as [], never null, so
// schema-checking MCP clients accept it.
func TestFileEmptyResultSerialisesArraysNotNull(t *testing.T) {
	db, p := testDB(t)
	out, err := File(db, p, "nothing.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"sessions":[]`) {
		t.Errorf("json = %s, want sessions:[]", b)
	}

	seedSession(t, db, p, "s", 2)
	exec1(t, db, `INSERT INTO file_touches(session_id, project_id, path, action) VALUES ('s', ?, 'x.go', 'edit')`, p)
	out, _ = File(db, p, "x.go", 10)
	b, _ = json.Marshal(out)
	if strings.Contains(string(b), "null") {
		t.Errorf("json = %s, want no nulls", b)
	}
	// Reading through the normalised path still finds it.
	out, _ = File(db, p, "./x.go", 10)
	if len(out.Sessions) != 1 {
		t.Errorf("./x.go found %d sessions, want 1", len(out.Sessions))
	}
}

func TestFileIsScopedToTheProject(t *testing.T) {
	db, p := testDB(t)
	res, _ := db.Exec(`INSERT INTO projects(name) VALUES ('other')`)
	other, _ := res.LastInsertId()
	seedSession(t, db, other, "theirs", 2)
	touch(t, db, other, "theirs", 0, "a.go", "edit", "2026-01-01T00:00:00Z")
	out, _ := File(db, p, "a.go", 10)
	if len(out.Sessions) != 0 {
		t.Errorf("leaked %d sessions from another project", len(out.Sessions))
	}
}

func TestNormalizePath(t *testing.T) {
	for in, want := range map[string]string{
		"a.go":                 "a.go",
		"./a.go":               "a.go",
		"internal//store/x.go": "internal/store/x.go",
		"internal/./x.go":      "internal/x.go",
		`internal\store\x.go`:  "internal/store/x.go",
		"  a.go ":              "a.go",
		"":                     "",
	} {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvePathRebasesOntoTheRepoRoot(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub := filepath.Join(root, "internal", "store")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// TempDir may sit behind a symlink; git reports the real path.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
		sub = filepath.Join(root, "internal", "store")
	}

	cases := []struct{ name, cwd, in, want string }{
		{"relative from a subdirectory", sub, "store.go", "internal/store/store.go"},
		{"parent-relative", sub, "../../main.go", "main.go"},
		{"absolute inside the repo", sub, filepath.Join(root, "cmd", "x.go"), "cmd/x.go"},
		{"already root-relative from the root", root, "cmd/x.go", "cmd/x.go"},
		{"absolute outside the repo stays as given", sub, "/etc/hosts", "/etc/hosts"},
		{"escaping the repo stays as given", root, "../elsewhere.go", "../elsewhere.go"},
	}
	for _, c := range cases {
		if got := ResolvePath(c.cwd, c.in); got != c.want {
			t.Errorf("%s: ResolvePath(%q, %q) = %q, want %q", c.name, c.cwd, c.in, got, c.want)
		}
	}

	// The MCP variant must NOT rebase relative paths: they are already
	// project-relative even when the server started in a subdirectory.
	if got := ProjectPath(sub, "internal/store/store.go"); got != "internal/store/store.go" {
		t.Errorf("ProjectPath relative = %q", got)
	}
	if got := ProjectPath(sub, filepath.Join(root, "cmd", "x.go")); got != "cmd/x.go" {
		t.Errorf("ProjectPath absolute = %q", got)
	}
}

func TestResolvePathOutsideAGitRepoUsesCwd(t *testing.T) {
	dir := t.TempDir()
	if got := ResolvePath(dir, "x.go"); got != "x.go" {
		t.Errorf("got %q, want x.go", got)
	}
}

// Commits that are only a best guess must say so, in the data an agent reads
// and not just in the CLI.
func TestFileFlagsApproximateCommitSpans(t *testing.T) {
	db, p := testDB(t)
	for id, src := range map[string]any{"s-branch": "branch", "s-head": "head", "s-unv": "unverified", "s-none": nil, "s-logged": "logged"} {
		seedSession(t, db, p, id, 2)
		exec1(t, db, `UPDATE sessions SET commit_source = ?, starting_commit = 'abc', ending_commit = 'def' WHERE id = ?`, src, id)
		touch(t, db, p, id, 0, "a.go", "edit", "2026-01-01T00:00:00Z")
	}
	out, err := File(db, p, "a.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Session{}
	for _, s := range out.Sessions {
		got[s.SessionID] = s
	}
	for id, want := range map[string]bool{"s-branch": false, "s-head": true, "s-unv": true, "s-none": false, "s-logged": false} {
		if got[id].CommitApproximate != want {
			t.Errorf("%s: CommitApproximate = %v, want %v", id, got[id].CommitApproximate, want)
		}
	}
	if got["s-head"].CommitSource != "head" || got["s-none"].CommitSource != "" {
		t.Errorf("CommitSource = %q / %q", got["s-head"].CommitSource, got["s-none"].CommitSource)
	}
	b, _ := json.Marshal(got["s-head"])
	if !strings.Contains(string(b), `"commit_approximate":true`) {
		t.Errorf("json = %s, want commit_approximate:true", b)
	}
}
