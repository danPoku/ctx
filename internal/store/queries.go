// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"
	"io/fs"
	"strings"
)

// LoadQuery extracts the SQL body of a named query from a sqlc-format file
// (queries/retrieval.sql), so that file stays the literal, single source of
// truth actually executed at runtime rather than a hand-copied duplicate
// baked into Go code. This is deliberately not sqlc codegen — that's worth
// adopting once the query surface (MCP tools, milestone 4) is big enough
// for the generated boilerplate to pay for itself; for now, one query
// (SearchKeyword) doesn't need it.
func LoadQuery(fsys fs.FS, file, name string) (string, error) {
	b, err := fs.ReadFile(fsys, file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", file, err)
	}

	marker := "-- name: " + name + " "
	lines := strings.Split(string(b), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, marker) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return "", fmt.Errorf("query %q not found in %s", name, file)
	}

	var body []string
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "-- name: ") {
			break
		}
		body = append(body, l)
	}
	sql := strings.TrimSpace(strings.Join(body, "\n"))
	if sql == "" {
		return "", fmt.Errorf("query %q in %s has no body", name, file)
	}
	return sql, nil
}
