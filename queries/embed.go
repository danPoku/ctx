// Package queries embeds the named SQL query files (sqlc-format:
// "-- name: X :many" sections) so the binary is self-contained and the .sql
// text itself — not a hand-copied Go duplicate of it — is what actually
// runs. See store.LoadQuery for how a named section is extracted.
package queries

import "embed"

//go:embed *.sql
var FS embed.FS
