package ingest

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kojog/ctx/internal/redact"
)

// FileResult is one file's outcome from RunAll.
type FileResult struct {
	Path string
	Err  error
}

// RunAll walks root for *.jsonl files and calls Run on each. A missing root
// (no sessions ingested yet on this machine) is not an error — it just
// yields no results.
func RunAll(db *sql.DB, adapter Adapter, root string) ([]FileResult, error) {
	var results []FileResult
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		results = append(results, FileResult{Path: path, Err: Run(db, adapter, path)})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return results, nil
	}
	return results, err
}

// Run processes one log file: reads new bytes since the source's last byte
// offset, parses each complete line, upserts the session(s) it belongs to,
// inserts new messages, and chunks any turns that now look settled. A line
// that fails to parse is logged to sources.last_error and skipped — never
// fatal to the rest of the file.
func Run(db *sql.DB, adapter Adapter, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	src, err := getOrCreateSource(db, adapter.Agent(), path)
	if err != nil {
		return err
	}

	byteOffset := src.ByteOffset
	if byteOffset > 0 {
		// The fingerprint is the hash of the bytes we'd ALREADY consumed as
		// of the last run (capped at 4KB) — never "whatever's in the first
		// 4KB right now". A file under 4KB total grows on every legitimate
		// append, so hashing "the current first 4KB" would change on every
		// append too and falsely look rewritten. Hashing only the prefix we
		// already committed to means it can only change if that specific,
		// already-read span was actually altered.
		windowHash, err := headHashUpTo(f, byteOffset)
		if err != nil {
			return err
		}
		if src.ContentHeadHash != "" && src.ContentHeadHash != windowHash {
			// The file was rewritten or rotated since we last read it: our
			// bookmark no longer means anything, start over.
			byteOffset = 0
		}
	}
	if byteOffset > info.Size() {
		// The file shrank (truncated/replaced): same story.
		byteOffset = 0
	}

	if _, err := f.Seek(byteOffset, io.SeekStart); err != nil {
		return err
	}

	r := bufio.NewReaderSize(f, 64*1024)
	toolNames := map[string]string{} // tool_use id -> tool name, this run only; see adapter doc
	sessionSeq := map[string]int{}   // session id -> next seq to assign
	touched := map[string]bool{}
	var lastErr string
	consumed := byteOffset

	for {
		lineBytes, readErr := r.ReadBytes('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				// A non-empty tail with no trailing newline is a line the
				// writer hasn't finished yet (this file can be actively
				// growing). Leave it — and the byte offset — for the next
				// run rather than process a truncated JSON object.
				break
			}
			lastErr = readErr.Error()
			break
		}
		consumed += int64(len(lineBytes))
		trimmed := bytes.TrimRight(lineBytes, "\n")
		if len(bytes.TrimSpace(trimmed)) == 0 {
			continue
		}
		sid, err := processLine(db, adapter, trimmed, toolNames, sessionSeq)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if sid != "" {
			touched[sid] = true
		}
	}

	for sid := range touched {
		if err := chunkSession(db, sid); err != nil {
			lastErr = err.Error()
		}
	}

	newHeadHash, err := headHashUpTo(f, consumed)
	if err != nil {
		return err
	}
	return updateSource(db, src.ID, consumed, newHeadHash, lastErr)
}

// headHashUpTo hashes the first min(4096, offset) bytes of f, read from the
// start regardless of f's current read position (ReadAt doesn't move it).
func headHashUpTo(f *os.File, offset int64) (string, error) {
	n := offset
	if n > 4096 {
		n = 4096
	}
	buf := make([]byte, n)
	if n > 0 {
		if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	return sha256Hex(buf), nil
}

// processLine parses one line, upserts the session it belongs to, and
// inserts its message if it has one. It returns the session id touched (for
// chunking afterward), or "" if the line carried no session information at
// all (shouldn't happen for a well-formed conversational or ai-title line,
// but Skip lines legitimately return "").
func processLine(db *sql.DB, adapter Adapter, line []byte, toolNames map[string]string, sessionSeq map[string]int) (string, error) {
	res, err := adapter.ParseLine(line)
	if err != nil {
		return "", err
	}
	if res.Skip {
		return "", nil
	}
	if res.Session.NativeID == "" {
		return "", nil
	}

	sessionID := adapter.Agent() + ":" + res.Session.NativeID
	if err := upsertSession(db, sessionID, adapter.Agent(), res.Session, res.Message); err != nil {
		return sessionID, err
	}

	if res.Message == nil {
		return sessionID, nil
	}
	msg := *res.Message

	// Resolve a tool_result's name from the tool_call that produced it.
	// This only sees calls from earlier in THIS ingest run — if a call and
	// its result straddle two separate one-shot runs (the file was ingested
	// mid-turn), the name is lost. Acceptable for a one-shot batch ingester;
	// closed properly once ctx daemon keeps this map alive continuously.
	if msg.Kind == "tool_result" {
		if name, ok := toolNames[msg.ToolUseID]; ok {
			msg.ToolName = name
		}
	}
	if msg.Kind == "tool_call" && msg.ToolUseID != "" {
		toolNames[msg.ToolUseID] = msg.ToolName
	}

	msg.Content = redact.Redact(msg.Content)
	redactedRaw := redact.Redact(string(msg.Raw))

	seq, ok := sessionSeq[sessionID]
	if !ok {
		var maxSeq sql.NullInt64
		if err := db.QueryRow(`SELECT MAX(seq) FROM messages WHERE session_id = ?`, sessionID).Scan(&maxSeq); err != nil {
			return sessionID, err
		}
		seq = 0
		if maxSeq.Valid {
			seq = int(maxSeq.Int64) + 1
		}
	}
	sessionSeq[sessionID] = seq + 1

	_, err = db.Exec(`INSERT OR IGNORE INTO messages(session_id, seq, role, kind, tool_name, content, created_at, raw)
	                   VALUES (?,?,?,?,?,?,?,?)`,
		sessionID, seq, msg.Role, msg.Kind, nullIfEmpty(msg.ToolName), msg.Content, nullIfEmpty(msg.CreatedAt), redactedRaw)
	return sessionID, err
}
