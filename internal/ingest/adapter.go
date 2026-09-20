// Package ingest is the agent-agnostic ingest driver: byte-offset
// bookkeeping per source file, session upsert, message insertion, and
// chunking. It knows nothing about any particular agent's log format — that
// lives in a per-agent Adapter (internal/ingest/claudecode today, Codex
// later) that turns one raw log line into the normalized shapes below.
package ingest

// RawMessage is one adapter-normalized event extracted from a single log
// line, ready for redaction and insertion into the messages table.
// Everything an agent's log format needs to express funnels through this
// shape so the driver never has to know which agent produced it.
type RawMessage struct {
	Role      string // "user" | "assistant" | "system" | "tool" (messages.role)
	Kind      string // "text" | "tool_call" | "tool_result" | "thinking" | "event"
	ToolName  string // set for tool_call / tool_result; may be resolved later, see ToolUseID
	ToolUseID string // present on tool_call and tool_result blocks; not persisted directly,
	// used by the driver to correlate a tool_result back to the tool_call
	// that produced it (the log format only carries the name on the call).
	Content   string // cleaned text, redacted before insert
	Raw       []byte // the original log line, redacted before insert
	CreatedAt string // ISO-8601 UTC, from the log line's own timestamp
}

// SessionUpdate carries session-level fields discovered on a log line that
// isn't itself a conversational message (an agent-provided title event) or
// that ride along with one (cwd, branch, model). Zero-value fields mean "no
// new information" and leave the existing column alone.
type SessionUpdate struct {
	NativeID  string // the agent's own session id — required on every non-skip line
	CWD       string
	GitBranch string
	Version   string
	Model     string
	Title     string
	Timestamp string
}

// ParseResult is what an Adapter returns for one input line.
type ParseResult struct {
	// Message is nil when the line carries only a session-level update (an
	// ai-title event) and nothing to store in messages.
	Message *RawMessage
	Session SessionUpdate
	// Skip is true for pure UI/harness bookkeeping (mode changes,
	// permission-mode, environment attachments, ...) that isn't
	// conversation and carries no session update either.
	Skip bool
}

// Adapter turns one agent's raw log format into the driver's normalized
// shapes. Implementations must never panic on malformed input — return an
// error instead, which the driver logs to sources.last_error and continues
// past.
type Adapter interface {
	// Agent is this adapter's value for sessions.agent / sources.agent,
	// e.g. "claude-code".
	Agent() string
	// ParseLine parses one line (without its trailing newline) of the
	// agent's log format.
	ParseLine(line []byte) (ParseResult, error)
}
