// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"database/sql"

	"github.com/danPoku/ctx/internal/store"
)

// upsertSession creates sessionID's row on first sight and otherwise
// updates only the fields this line actually carries new information for.
// msg is the conversational message this update rode in on, if any — used
// purely to seed the title fallback ("first user prompt, trimmed") the
// first time a session is created with no agent-provided title yet.
//
// It returns the session's project_id (nil if unresolved) so callers that
// need it — recording a file_touches row alongside a tool_call message —
// don't have to re-query it themselves on every message.
func upsertSession(db *sql.DB, sessionID, agent string, upd SessionUpdate, msg *RawMessage) (any, error) {
	var projectID sql.NullInt64
	err := db.QueryRow(`SELECT project_id FROM sessions WHERE id = ?`, sessionID).Scan(&projectID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		_, err := db.Exec(`UPDATE sessions SET
		                       cwd        = COALESCE(NULLIF(?, ''), cwd),
		                       git_branch = COALESCE(NULLIF(?, ''), git_branch),
		                       model      = COALESCE(NULLIF(?, ''), model),
		                       title      = COALESCE(NULLIF(?, ''), title),
		                       ended_at   = CASE WHEN ? > COALESCE(ended_at, '') THEN ? ELSE ended_at END
		                     WHERE id = ?`,
			upd.CWD, upd.GitBranch, upd.Model, upd.Title, upd.Timestamp, upd.Timestamp, sessionID)
		return nullInt64ToAny(projectID), err
	}

	// project_id stays NULL if we've never seen a cwd for this session —
	// shouldn't happen with real Claude Code logs, but resolving "" would
	// otherwise create a bogus project named ".".
	var newProjectID any
	if upd.CWD != "" {
		id, err := store.ResolveProject(db, upd.CWD)
		if err != nil {
			return nil, err
		}
		newProjectID = id
	}

	title := upd.Title
	if title == "" && msg != nil && msg.Role == "user" && msg.Kind == "text" {
		title = truncate(msg.Content, 80)
	}

	_, err = db.Exec(`INSERT INTO sessions(id, agent, native_id, project_id, cwd, git_branch, model, started_at, ended_at, title)
	                   VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sessionID, agent, upd.NativeID, newProjectID,
		nullIfEmpty(upd.CWD), nullIfEmpty(upd.GitBranch), nullIfEmpty(upd.Model),
		upd.Timestamp, upd.Timestamp, nullIfEmpty(title))
	return newProjectID, err
}

func nullInt64ToAny(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
