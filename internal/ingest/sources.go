package ingest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
)

type sourceRow struct {
	ID              int64
	ByteOffset      int64
	ContentHeadHash string
}

// getOrCreateSource looks up path's bookkeeping row (the mailroom log entry
// for this file), creating one at byte_offset 0 the first time this file is
// seen.
func getOrCreateSource(db *sql.DB, agent, path string) (sourceRow, error) {
	var s sourceRow
	var hash sql.NullString
	err := db.QueryRow(`SELECT id, byte_offset, content_head_hash FROM sources WHERE path = ?`, path).
		Scan(&s.ID, &s.ByteOffset, &hash)
	if err == nil {
		s.ContentHeadHash = hash.String
		return s, nil
	}
	if err != sql.ErrNoRows {
		return sourceRow{}, err
	}

	res, err := db.Exec(`INSERT INTO sources(agent, path, byte_offset) VALUES (?, ?, 0)`, agent, path)
	if err != nil {
		return sourceRow{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return sourceRow{}, err
	}
	return sourceRow{ID: id}, nil
}

func updateSource(db *sql.DB, id, byteOffset int64, headHash, lastErr string) error {
	_, err := db.Exec(`UPDATE sources
	                       SET byte_offset = ?, content_head_hash = ?,
	                           last_ingested_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
	                           last_error = ?
	                     WHERE id = ?`,
		byteOffset, headHash, nullIfEmpty(lastErr), id)
	return err
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
