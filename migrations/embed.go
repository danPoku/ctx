// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

// Package migrations embeds the SQL migration files so the ctx binary is
// self-contained: no separate install step has to ship .sql files alongside
// it. The .sql files themselves stay in this directory (not under
// internal/store) because go:embed can only reach files inside its own
// package's directory tree, and this is also where queries/*.sql-adjacent
// tooling (sqlc) expects schema files to live.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
