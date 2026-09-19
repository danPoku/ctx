// Package store manages the ctx SQLite database: opening it with the
// connection pragmas every ctx component depends on, and applying the
// embedded schema migrations.
package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/kojog/ctx/migrations"
)

// The sqlite-vec extension (the vec0 virtual table module) isn't wired up
// yet — that's milestone 5 (embedding worker + hybrid search), and mixing
// its cgo bindings with mattn/go-sqlite3's bundled amalgamation needs its
// own header plumbing that doesn't belong in this milestone. Until then,
// 002_vectors.sql's CREATE VIRTUAL TABLE ... USING vec0(...) fails with "no
// such module: vec0" on every run, which is exactly the case Migrate below
// is built to log-and-continue past.

// Open opens (creating if necessary) the SQLite database at path with the
// four connection pragmas required by every ctx component: journal_mode=WAL,
// foreign_keys=ON, busy_timeout=5000, synchronous=NORMAL.
//
// The pragmas ride on the DSN rather than a post-Open Exec. database/sql
// pools connections lazily and opens more of them under concurrent load, and
// PRAGMA journal_mode/foreign_keys/synchronous are per-connection state in
// SQLite — an Exec run once right after Open only ever reaches whichever
// single connection the pool happened to create first. Putting the pragmas
// in the DSN's query string makes mattn/go-sqlite3 apply them to every
// connection it opens, present and future.
func Open(path string) (*sql.DB, error) {
	dsn := path + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	return db, nil
}

// MigrateResult reports what Migrate actually applied, so callers (ctx init,
// tests) can tell whether semantic search is available without parsing error
// strings.
type MigrateResult struct {
	VectorsLoaded bool
	VectorsErr    error // non-nil iff !VectorsLoaded; the reason, for logging
}

// Migrate applies the embedded schema migrations. 001_core.sql is
// non-optional: sessions, messages, keyword search, notes all depend on it,
// so a failure there is fatal. 002_vectors.sql needs the sqlite-vec
// extension's vec0 virtual table module; if that module isn't available the
// CREATE VIRTUAL TABLE statement fails with "no such module: vec0", and
// Migrate reports that in the result instead of returning an error — the
// catalogue stays open even when the "find similar cases" desk is closed.
//
// Both migration files are idempotent at the SQL level (CREATE ... IF NOT
// EXISTS, INSERT OR IGNORE), so calling Migrate more than once on the same
// database is a no-op, not an error.
func Migrate(db *sql.DB) (MigrateResult, error) {
	core, err := migrations.FS.ReadFile("001_core.sql")
	if err != nil {
		return MigrateResult{}, fmt.Errorf("read 001_core.sql: %w", err)
	}
	if _, err := db.Exec(string(core)); err != nil {
		return MigrateResult{}, fmt.Errorf("apply 001_core.sql: %w", err)
	}

	vectors, err := migrations.FS.ReadFile("002_vectors.sql")
	if err != nil {
		return MigrateResult{}, fmt.Errorf("read 002_vectors.sql: %w", err)
	}
	if _, err := db.Exec(string(vectors)); err != nil {
		return MigrateResult{VectorsLoaded: false, VectorsErr: err}, nil
	}
	return MigrateResult{VectorsLoaded: true}, nil
}
