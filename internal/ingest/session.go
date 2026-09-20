package ingest

import (
	"database/sql"

	"github.com/kojog/ctx/internal/store"
)

// upsertSession creates sessionID's row on first sight and otherwise
// updates only the fields this line actually carries new information for.
// msg is the conversational message this update rode in on, if any — used
// purely to seed the title fallback ("first user prompt, trimmed") the
// first time a session is created with no agent-provided title yet.
func upsertSession(db *sql.DB, sessionID, agent string, upd SessionUpdate, msg *RawMessage) error {
	var exists bool
	err := db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, sessionID).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return err
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
		return err
	}

	// project_id stays NULL if we've never seen a cwd for this session —
	// shouldn't happen with real Claude Code logs, but resolving "" would
	// otherwise create a bogus project named ".".
	var projectID any
	if upd.CWD != "" {
		id, err := store.ResolveProject(db, upd.CWD)
		if err != nil {
			return err
		}
		projectID = id
	}

	title := upd.Title
	if title == "" && msg != nil && msg.Role == "user" && msg.Kind == "text" {
		title = truncate(msg.Content, 80)
	}

	_, err = db.Exec(`INSERT INTO sessions(id, agent, native_id, project_id, cwd, git_branch, model, started_at, ended_at, title)
	                   VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sessionID, agent, upd.NativeID, projectID,
		nullIfEmpty(upd.CWD), nullIfEmpty(upd.GitBranch), nullIfEmpty(upd.Model),
		upd.Timestamp, upd.Timestamp, nullIfEmpty(title))
	return err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
