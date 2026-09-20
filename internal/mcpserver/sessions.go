package mcpserver

import (
	"context"
	"database/sql"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kojog/ctx/internal/store"
	"github.com/kojog/ctx/queries"
)

const defaultRecentLimit = 20

// getSessionMaxRows caps how many messages get_session can return in one
// call — progressive disclosure: an agent pages through a session
// deliberately instead of one call swallowing a 2,000-message transcript
// into its context window.
const getSessionMaxRows = 200

type recentSessionsArgs struct {
	Agent string `json:"agent,omitempty" jsonschema:"restrict to one agent, e.g. claude-code or codex (default: every agent)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max sessions, default 20"`
}

type sessionOverview struct {
	ID              string `json:"id"`
	Agent           string `json:"agent"`
	GitBranch       string `json:"git_branch,omitempty"`
	Title           string `json:"title,omitempty"`
	Summary         string `json:"summary,omitempty"`
	StartedAt       string `json:"started_at"`
	EndedAt         string `json:"ended_at,omitempty"`
	MessageCount    int    `json:"message_count"`
	FilesChanged    int    `json:"files_changed"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

type recentSessionsResult struct {
	Sessions []sessionOverview `json:"sessions"`
}

type getSessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"the session id, e.g. as returned by search_context or recent_sessions"`
	FromSeq   int    `json:"from_seq,omitempty" jsonschema:"first message sequence number to return, default 0"`
	ToSeq     int    `json:"to_seq,omitempty" jsonschema:"last message sequence number to return; the range is capped, so a large session needs multiple calls"`
}

type sessionMessage struct {
	Seq       int    `json:"seq"`
	Role      string `json:"role"`
	Kind      string `json:"kind"`
	ToolName  string `json:"tool_name,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

type getSessionResult struct {
	Messages []sessionMessage `json:"messages"`
	// Truncated and NextFromSeq tell the caller there's more beyond what
	// was returned, and where to resume — the paging contract that keeps
	// this tool from being used to swallow a whole transcript in one call.
	Truncated   bool `json:"truncated"`
	NextFromSeq *int `json:"next_from_seq,omitempty"`
}

type sessionsTouchingArgs struct {
	Path  string `json:"path" jsonschema:"project-relative file path, e.g. internal/store/store.go"`
	Limit int    `json:"limit,omitempty" jsonschema:"max sessions, default 20"`
}

type fileTouchSession struct {
	SessionID string   `json:"session_id"`
	Agent     string   `json:"agent"`
	Title     string   `json:"title,omitempty"`
	StartedAt string   `json:"started_at"`
	Actions   []string `json:"actions"`
	Touches   int      `json:"touches"`
}

type sessionsTouchingResult struct {
	Sessions []fileTouchSession `json:"sessions"`
}

func (s *server) registerSessions(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recent_sessions",
		Description: "List the most recent coding-agent sessions in this project, newest first. A summary view — use get_session to read one in full.",
	}, s.recentSessions)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_session",
		Description: "Read a session's messages in order, paged by sequence number. Capped per call — check `truncated` and use `next_from_seq` to keep paging through a long session.",
	}, s.getSession)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sessions_touching",
		Description: "Find past sessions that touched a given file, most recent first — useful before editing a file to see who's been here and what they did. Path must be project-relative.",
	}, s.sessionsTouching)
}

func (s *server) recentSessions(_ context.Context, _ *mcp.CallToolRequest, args recentSessionsArgs) (*mcp.CallToolResult, recentSessionsResult, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = defaultRecentLimit
	}
	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", "RecentSessions")
	if err != nil {
		return nil, recentSessionsResult{}, err
	}

	var agentArg any
	if args.Agent != "" {
		agentArg = args.Agent
	}
	rows, err := s.db.Query(sqlText,
		sql.Named("project_id", s.projectID), sql.Named("agent", agentArg), sql.Named("limit", limit))
	if err != nil {
		return nil, recentSessionsResult{}, err
	}
	defer rows.Close()

	var out recentSessionsResult
	for rows.Next() {
		// v_session_overview: id, agent, project_id, project, git_branch,
		// title, summary, started_at, ended_at, message_count,
		// files_changed, parent_session_id
		var o sessionOverview
		var projectID sql.NullInt64
		var project, gitBranch, title, summary, endedAt, parentSessionID sql.NullString
		if err := rows.Scan(&o.ID, &o.Agent, &projectID, &project, &gitBranch, &title, &summary,
			&o.StartedAt, &endedAt, &o.MessageCount, &o.FilesChanged, &parentSessionID); err != nil {
			return nil, recentSessionsResult{}, err
		}
		o.GitBranch, o.Title, o.Summary = gitBranch.String, title.String, summary.String
		o.EndedAt, o.ParentSessionID = endedAt.String, parentSessionID.String
		out.Sessions = append(out.Sessions, o)
	}
	return nil, out, rows.Err()
}

func (s *server) getSession(_ context.Context, _ *mcp.CallToolRequest, args getSessionArgs) (*mcp.CallToolResult, getSessionResult, error) {
	fromSeq := args.FromSeq
	toSeq := args.ToSeq
	if toSeq <= 0 || toSeq < fromSeq {
		toSeq = fromSeq + getSessionMaxRows - 1
	}
	truncated := false
	if toSeq-fromSeq+1 > getSessionMaxRows {
		toSeq = fromSeq + getSessionMaxRows - 1
		truncated = true
	}

	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", "GetSessionMessages")
	if err != nil {
		return nil, getSessionResult{}, err
	}
	rows, err := s.db.Query(sqlText,
		sql.Named("session_id", args.SessionID), sql.Named("from_seq", fromSeq), sql.Named("to_seq", toSeq))
	if err != nil {
		return nil, getSessionResult{}, err
	}
	defer rows.Close()

	var out getSessionResult
	lastSeq := fromSeq - 1
	for rows.Next() {
		var m sessionMessage
		var toolName, createdAt sql.NullString
		if err := rows.Scan(&m.Seq, &m.Role, &m.Kind, &toolName, &m.Content, &createdAt); err != nil {
			return nil, getSessionResult{}, err
		}
		m.ToolName, m.CreatedAt = toolName.String, createdAt.String
		out.Messages = append(out.Messages, m)
		lastSeq = m.Seq
	}
	if err := rows.Err(); err != nil {
		return nil, getSessionResult{}, err
	}

	// Also treat hitting the requested/capped range's ceiling as
	// "truncated" even if the caller's own to_seq wasn't past the cap —
	// there may be more messages beyond it they didn't ask for yet.
	if lastSeq >= toSeq {
		truncated = true
	}
	out.Truncated = truncated
	if truncated {
		next := lastSeq + 1
		out.NextFromSeq = &next
	}
	return nil, out, nil
}

func (s *server) sessionsTouching(_ context.Context, _ *mcp.CallToolRequest, args sessionsTouchingArgs) (*mcp.CallToolResult, sessionsTouchingResult, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = defaultRecentLimit
	}
	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", "SessionsTouchingFile")
	if err != nil {
		return nil, sessionsTouchingResult{}, err
	}
	rows, err := s.db.Query(sqlText,
		sql.Named("project_id", s.projectID), sql.Named("path", args.Path), sql.Named("limit", limit))
	if err != nil {
		return nil, sessionsTouchingResult{}, err
	}
	defer rows.Close()

	var out sessionsTouchingResult
	for rows.Next() {
		var f fileTouchSession
		var title, actions sql.NullString
		if err := rows.Scan(&f.SessionID, &f.Agent, &title, &f.StartedAt, &actions, &f.Touches); err != nil {
			return nil, sessionsTouchingResult{}, err
		}
		f.Title = title.String
		if actions.Valid && actions.String != "" {
			f.Actions = strings.Split(actions.String, ",")
		}
		out.Sessions = append(out.Sessions, f)
	}
	return nil, out, rows.Err()
}
