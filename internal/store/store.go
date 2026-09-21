// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

// Package store manages the ctx SQLite database: opening it with the
// connection pragmas every ctx component depends on, and applying the
// embedded schema migrations.
package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/danPoku/kaectx/internal/vecext"
	"github.com/danPoku/kaectx/migrations"
)

func init() {
	// Registers sqlite-vec's vec0 module on every SQLite connection opened
	// in this process from here on. Must run before Open is ever called.
	// See internal/vecext's doc for why this needs a vendored package
	// rather than importing sqlite-vec's own Go bindings directly. If the
	// module still can't be used for some reason, that surfaces later as a
	// "no such module: vec0" error when 002_vectors.sql runs, which Migrate
	// treats as optional rather than fatal.
	vecext.Register()
}

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
