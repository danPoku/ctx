package ingest

import (
	"database/sql"
	"strings"
)

type turnRow struct {
	seq              int
	role, kind, text string
}

// chunkSession turns newly-inserted messages for sessionID into chunks (one
// user turn + the assistant's reply, tool noise trimmed — see
// migrations/001_core.sql's comment on the chunks table).
//
// The boundary rule:
//   - A chunk closes the moment the NEXT user text message arrives — that's
//     unambiguous proof the previous turn is over, whether or not the
//     assistant ever replied with text of its own.
//   - The trailing, still-open buffer at end-of-input only closes if it
//     looks settled: its last row is an assistant text message, i.e. the
//     assistant appears to have finished rather than being mid-tool-call.
//     An unsettled tail is left for the next ingest run to complete.
//
// This makes chunking purely insert-only: no chunk is ever revised once
// written, which is what keeps re-running ingest idempotent without extra
// bookkeeping beyond "start after the last chunk's last_seq".
func chunkSession(db *sql.DB, sessionID string) error {
	var startSeq int
	if err := db.QueryRow(`SELECT COALESCE(MAX(last_seq) + 1, 0) FROM chunks WHERE session_id = ?`, sessionID).
		Scan(&startSeq); err != nil {
		return err
	}

	rows, err := db.Query(`SELECT seq, role, kind, content FROM messages
	                          WHERE session_id = ? AND seq >= ?
	                          ORDER BY seq`, sessionID, startSeq)
	if err != nil {
		return err
	}
	defer rows.Close()

	var buf []turnRow

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		defer func() { buf = nil }()
		return insertChunkIfQualifying(db, sessionID, buf)
	}

	for rows.Next() {
		var r turnRow
		if err := rows.Scan(&r.seq, &r.role, &r.kind, &r.text); err != nil {
			return err
		}
		if r.role == "user" && r.kind == "text" && len(buf) > 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		buf = append(buf, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if n := len(buf); n > 0 && buf[n-1].role == "assistant" && buf[n-1].kind == "text" {
		if err := flush(); err != nil {
			return err
		}
	}
	return nil
}

func insertChunkIfQualifying(db *sql.DB, sessionID string, buf []turnRow) error {
	var parts []string
	for _, r := range buf {
		if r.kind != "text" {
			continue
		}
		switch r.role {
		case "user":
			parts = append(parts, "User: "+r.text)
		case "assistant":
			parts = append(parts, "Assistant: "+r.text)
		}
	}
	if len(parts) == 0 {
		// Nothing worth indexing in this stretch (a lone system notice, or
		// a run of tool calls with no framing text) — leave it out of the
		// search index. It's still preserved in full in `messages`.
		return nil
	}
	text := strings.Join(parts, "\n\n")
	_, err := db.Exec(`INSERT OR IGNORE INTO chunks(session_id, first_seq, last_seq, text) VALUES (?,?,?,?)`,
		sessionID, buf[0].seq, buf[len(buf)-1].seq, text)
	return err
}
