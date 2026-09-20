// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

// Package vecext makes SQLite's vec0 virtual table module (from
// github.com/asg017/sqlite-vec) available to mattn/go-sqlite3 connections.
//
// Why this package exists instead of importing sqlite-vec's own Go bindings
// directly: those bindings' C code does `#include "sqlite3.h"`, but
// mattn/go-sqlite3 bundles its SQLite amalgamation under the filename
// sqlite3-binding.h instead — there's no "sqlite3.h" on the include path
// for it to find. Discovered in milestone 1 (see internal/store's history)
// and deferred until an actual embedding worker needed chunk_vectors to
// exist.
//
// The fix: vendor sqlite-vec's C source alongside a COPY of
// mattn/go-sqlite3's header renamed to sqlite3.h, and compile them as part
// of THIS package with our own #cgo CFLAGS -I pointing at that copy. cgo
// flags are scoped per package, so we can't inject an -I into the upstream
// bindings' compilation — but we don't need to: C symbol resolution happens
// at the final link, not per-package compile, so sqlite-vec.c only needs
// SOME header with matching declarations to compile against. The actual
// sqlite3_* function bodies still come from mattn's compiled amalgamation,
// since both end up as object files linked into the same Go binary. Using
// an exact copy of mattn's own header (not a generic sqlite3.h from
// sqlite.org) guarantees the declarations match mattn's actual SQLite
// build, not just "a" SQLite build.
//
// sqlite-vec.c/sqlite-vec.h are vendored verbatim from
// github.com/asg017/sqlite-vec-go-bindings@v0.1.6 (MIT); sqlite3.h is an
// unmodified copy of github.com/mattn/go-sqlite3@v1.14.52's
// sqlite3-binding.h, renamed only so the #include resolves. Bump both
// alongside their real go.mod dependency versions if either changes.
package vecext

// #cgo CFLAGS: -DSQLITE_CORE -I${SRCDIR}
// #cgo LDFLAGS: -lm
// #include "sqlite-vec.h"
import "C"

// Register makes vec0 available on every SQLite connection opened in this
// process from here on, via sqlite3_auto_extension. Must be called before
// any *sql.DB is opened — internal/store does this in an init().
func Register() {
	C.sqlite3_auto_extension((*[0]byte)(C.sqlite3_vec_init))
}
