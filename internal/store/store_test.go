package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// expectedObjects is the full set of tables/views 001_core.sql must produce,
// keyed by name with the sqlite_master type it should carry.
// v_session_overview is a VIEW, not a TABLE — checking each name against its
// specific expected type (rather than a blanket type='table' filter) is the
// point: a filter that only looked for tables would miss a silently-failed
// view creation entirely.
var expectedObjects = map[string]string{
	"schema_migrations":  "table",
	"projects":           "table",
	"project_paths":      "table",
	"sources":            "table",
	"sessions":           "table",
	"messages":           "table",
	"chunks":             "table",
	"chunks_fts":         "table", // fts5 virtual tables register as type=table
	"file_touches":       "table",
	"notes":              "table",
	"notes_fts":          "table",
	"v_session_overview": "view",
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ctx.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateCreatesCoreSchema(t *testing.T) {
	db := openTestDB(t)
	if _, err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table','view')`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for name, wantType := range expectedObjects {
		gotType, ok := got[name]
		if !ok {
			t.Errorf("missing schema object %q", name)
			continue
		}
		if gotType != wantType {
			t.Errorf("%q: type = %q, want %q", name, gotType, wantType)
		}
	}
}

// TestPragmasAppliedToEveryConnection is the actual point of putting the
// pragmas on the DSN instead of running them once after Open: database/sql
// hands out whichever pooled connection is free, so a test that only ever
// touches one connection would pass even if the DSN's pragma params were
// silently ignored (mattn/go-sqlite3 does not error on an unrecognized
// _param). This forces several connections open concurrently and checks
// each one individually.
func TestPragmasAppliedToEveryConnection(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(4)

	ctx := context.Background()
	const n = 4
	conns := make([]*sql.Conn, n)
	for i := range conns {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		conns[i] = c
	}
	t.Cleanup(func() {
		for _, c := range conns {
			if c != nil {
				c.Close()
			}
		}
	})

	for i, c := range conns {
		var journalMode string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("conn %d journal_mode: %v", i, err)
		}
		if journalMode != "wal" {
			t.Errorf("conn %d: journal_mode = %q, want wal", i, journalMode)
		}

		var foreignKeys int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("conn %d foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, foreignKeys)
		}

		var busyTimeout int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("conn %d busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("conn %d: busy_timeout = %d, want 5000", i, busyTimeout)
		}

		var synchronous int
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatalf("conn %d synchronous: %v", i, err)
		}
		if synchronous != 1 { // 0=OFF, 1=NORMAL, 2=FULL, 3=EXTRA
			t.Errorf("conn %d: synchronous = %d, want 1 (NORMAL)", i, synchronous)
		}
	}
}

// TestMigrateTwiceIsANoOp doesn't assume whether sqlite-vec loads in the
// test environment: it reads VectorsLoaded from the first call and expects
// the second call to land on the same schema_migrations row count, whichever
// that is.
func TestMigrateTwiceIsANoOp(t *testing.T) {
	db := openTestDB(t)

	first, err := Migrate(db)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	want := 1
	if first.VectorsLoaded {
		want = 2
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != want {
		t.Fatalf("schema_migrations has %d rows after first Migrate, want %d", count, want)
	}

	second, err := Migrate(db)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if second.VectorsLoaded != first.VectorsLoaded {
		t.Errorf("VectorsLoaded changed between calls: first=%v second=%v", first.VectorsLoaded, second.VectorsLoaded)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations after second Migrate: %v", err)
	}
	if count != want {
		t.Errorf("schema_migrations has %d rows after second Migrate, want %d (idempotent)", count, want)
	}
}
