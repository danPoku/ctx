-- =============================================================================
-- ctx — local multi-agent context store
-- Migration 001: core tables, full-text search, sync triggers
-- =============================================================================
--
-- THE MENTAL MODEL
-- Think of an insurer's head office. Each branch (Claude Code, Codex, Pi, BB)
-- files claims its own way. Head office doesn't change the branches — it keeps:
--
--   sources       -> the mailroom log: which envelope we've opened, how far we read
--   projects      -> the client register: one row per real-world client (repo)
--   sessions      -> the claim form: one standardised record per conversation
--   messages      -> the claim's correspondence, in order, original kept on file
--   chunks        -> index cards cut from the correspondence, for fast lookup
--   file_touches  -> "which claims involved this property?" (which sessions edited auth.ts)
--   notes         -> the underwriting manual: distilled decisions and gotchas
--
-- CONVENTIONS
--   * All tables are STRICT: SQLite rejects a string in an INTEGER column instead
--     of silently storing it. Like a form that won't accept letters in the
--     phone-number box — annoying once, saves you forever.
--   * Timestamps are ISO-8601 UTC TEXT ('2026-09-19T10:15:00Z'). Sortable as
--     text, readable in any tool, no timezone surprises between WSL and Windows.
--   * JSON lives in TEXT columns guarded by CHECK(json_valid(...)). This is the
--     "document model" part: flexible where agents differ, strict where they don't.
--
-- CONNECTION PRAGMAS (set by the app on EVERY connection — they're not stored):
--   PRAGMA journal_mode = WAL;     -- daemon writes while agents read
--   PRAGMA foreign_keys = ON;      -- SQLite ships with FKs OFF by default
--   PRAGMA busy_timeout = 5000;    -- wait 5s for a lock instead of failing
--   PRAGMA synchronous = NORMAL;   -- safe with WAL, much faster than FULL
-- =============================================================================

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
) STRICT;

-- -----------------------------------------------------------------------------
-- projects — the client register
-- -----------------------------------------------------------------------------
-- A project is identified by its normalised git remote, NOT its folder path.
-- The same repo lives at /home/dan/code/x in WSL and /workspaces/x in a
-- Codespace; like one client with two addresses, it must stay one client.
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    -- normalised: 'github.com/dan/forex-model' (no scheme, no .git, lowercase host)
    -- NULL for folders that aren't git repos yet
    git_remote  TEXT    UNIQUE,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
) STRICT;

-- Every folder path we've seen a project at. The ingest daemon resolves an
-- agent's cwd -> project by longest matching prefix here.
CREATE TABLE IF NOT EXISTS project_paths (
    path        TEXT    PRIMARY KEY,            -- absolute, no trailing slash
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    first_seen  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
) STRICT;

-- -----------------------------------------------------------------------------
-- sources — the mailroom log (makes ingestion idempotent)
-- -----------------------------------------------------------------------------
-- Agent logs are append-only JSONL files. We remember how many bytes we've
-- read, like a bookmark. On restart the daemon seeks to byte_offset and reads
-- only what's new. If content_head_hash changes, the file was rewritten or
-- rotated, so we re-read from zero rather than trust a stale bookmark.
CREATE TABLE IF NOT EXISTS sources (
    id                 INTEGER PRIMARY KEY,
    agent              TEXT    NOT NULL,        -- 'claude-code' | 'codex' | 'pi' | 'bb' | ...
    path               TEXT    NOT NULL UNIQUE,
    byte_offset        INTEGER NOT NULL DEFAULT 0,
    -- hash of the first 4 KB: cheap fingerprint that detects rewrites
    content_head_hash  TEXT,
    last_ingested_at   TEXT,
    last_error         TEXT                     -- parse failures land here, not in a crash
) STRICT;

-- -----------------------------------------------------------------------------
-- sessions — the standardised claim form
-- -----------------------------------------------------------------------------
-- Primary key is namespaced ('codex:0199a...') so two agents can never
-- collide even if both happen to use UUIDs.
CREATE TABLE IF NOT EXISTS sessions (
    id                 TEXT    PRIMARY KEY,     -- '<agent>:<native_id>'
    agent              TEXT    NOT NULL,
    native_id          TEXT    NOT NULL,        -- the agent's own session id
    project_id         INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    source_id          INTEGER REFERENCES sources(id)  ON DELETE SET NULL,

    -- Subagents and forks: Claude Code spawns sidechains, Codex can resume.
    -- A child claim that references its parent file.
    parent_session_id  TEXT    REFERENCES sessions(id) ON DELETE SET NULL,

    cwd                TEXT,
    git_branch         TEXT,
    model              TEXT,                    -- e.g. 'claude-opus-5', last model seen
    started_at         TEXT    NOT NULL,
    ended_at           TEXT,                    -- NULL while the session is live
    title              TEXT,                    -- first user prompt, trimmed, or agent-provided
    summary            TEXT,                    -- written later by a cheap summariser model
    summary_model      TEXT,
    message_count      INTEGER NOT NULL DEFAULT 0,

    -- Agent-specific leftovers (approval mode, sandbox policy, cli version...).
    -- The escape hatch that keeps the columns above stable.
    meta               TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(meta)),

    created_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

    UNIQUE (agent, native_id)
) STRICT;

-- The two questions asked most: "latest sessions in this project" and
-- "latest sessions by this agent". Each index is one drawer in the cabinet.
CREATE INDEX IF NOT EXISTS idx_sessions_project_time ON sessions(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_agent_time   ON sessions(agent, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_parent       ON sessions(parent_session_id)
    WHERE parent_session_id IS NOT NULL;

-- -----------------------------------------------------------------------------
-- messages — the correspondence, in order
-- -----------------------------------------------------------------------------
-- `content` is the cleaned, REDACTED text we search and show.
-- `raw` is the original event (also redacted) so a smarter parser next month
-- can re-derive everything without re-reading the agent's files — the
-- photocopy on file behind the typed-up summary.
CREATE TABLE IF NOT EXISTS messages (
    id           INTEGER PRIMARY KEY,
    session_id   TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,              -- 0-based order within the session
    role         TEXT    NOT NULL CHECK (role IN ('user','assistant','system','tool')),
    kind         TEXT    NOT NULL DEFAULT 'text'
                 CHECK (kind IN ('text','tool_call','tool_result','thinking','event')),
    tool_name    TEXT,                          -- 'Edit', 'Bash', 'apply_patch', ...
    content      TEXT    NOT NULL DEFAULT '',
    tokens_in    INTEGER,
    tokens_out   INTEGER,
    created_at   TEXT,
    raw          TEXT    CHECK (raw IS NULL OR json_valid(raw)),
    UNIQUE (session_id, seq)                    -- re-ingesting the same line is a no-op
) STRICT;

CREATE INDEX IF NOT EXISTS idx_messages_tool ON messages(tool_name) WHERE tool_name IS NOT NULL;

-- -----------------------------------------------------------------------------
-- chunks — index cards for retrieval
-- -----------------------------------------------------------------------------
-- Why not search `messages` directly? A single message is often useless out
-- of context ("yes, do that"). A chunk is one exchange: the user's turn plus
-- the assistant's reply (tool noise trimmed), so each card makes sense alone.
--
-- Crucially, BOTH keyword search (FTS5) and vector search index THIS table.
-- Same unit, same ids -> the two rankings can be fused cleanly. Fusing
-- rankings of different things is like averaging a house's valuation with
-- its street's crime rate: numbers, but not comparable ones.
CREATE TABLE IF NOT EXISTS chunks (
    id               INTEGER PRIMARY KEY,
    session_id       TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    first_seq        INTEGER NOT NULL,
    last_seq         INTEGER NOT NULL,
    text             TEXT    NOT NULL,
    token_count      INTEGER,
    -- NULL until the embedder processes it: the embed worker's to-do list is
    -- simply "WHERE embedded_at IS NULL"
    embedded_at      TEXT,
    embedding_model  TEXT,                      -- lets you re-embed when you switch models
    CHECK (last_seq >= first_seq),
    UNIQUE (session_id, first_seq)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_chunks_pending ON chunks(id) WHERE embedded_at IS NULL;

-- Keyword index over chunks. "External content" mode: FTS stores only the
-- index, not a second copy of the text — the card catalogue points at the
-- shelf instead of photocopying every book.
--
-- Tokenizer: porter stemming ('refactoring' matches 'refactor') on top of
-- unicode61, with '_' kept inside tokens so snake_case identifiers like
-- `get_user_by_id` stay whole instead of splitting into four words.
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    text,
    content = 'chunks',
    content_rowid = 'id',
    tokenize = "porter unicode61 tokenchars '_'"
);

-- External-content FTS doesn't update itself: these triggers are the clerk
-- who updates the catalogue whenever a card is added, changed, or removed.
CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
END;
CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE OF text ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
END;

-- -----------------------------------------------------------------------------
-- file_touches — "which sessions worked on this file?"
-- -----------------------------------------------------------------------------
-- Extracted from tool calls (Edit/Write/apply_patch/Read). This is often the
-- sharpest retrieval key of all: an agent about to edit src/auth/token.go can
-- pull every past session that touched it, across all agents, before it starts.
CREATE TABLE IF NOT EXISTS file_touches (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id  INTEGER REFERENCES messages(id) ON DELETE CASCADE,
    project_id  INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    path        TEXT    NOT NULL,               -- project-relative: 'src/auth/token.go'
    action      TEXT    NOT NULL CHECK (action IN ('read','edit','create','delete','rename')),
    created_at  TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_touches_path    ON file_touches(project_id, path);
CREATE INDEX IF NOT EXISTS idx_touches_session ON file_touches(session_id);

-- -----------------------------------------------------------------------------
-- notes — the underwriting manual
-- -----------------------------------------------------------------------------
-- Transcripts are evidence; notes are policy. Agents write these deliberately
-- via `save_note` / `ctx note add`: "we chose pgx over database/sql because...",
-- "the test DB must be reset before integration tests".
--
-- Notes are never edited in place when the decision changes — a new note is
-- written and the old one points to it via superseded_by. Like an amended
-- policy clause: you keep the history of what was believed and when.
CREATE TABLE IF NOT EXISTS notes (
    id                 INTEGER PRIMARY KEY,
    project_id         INTEGER REFERENCES projects(id) ON DELETE CASCADE, -- NULL = global
    session_id         TEXT    REFERENCES sessions(id) ON DELETE SET NULL,  -- where it came from
    agent              TEXT,                    -- who wrote it (or 'human')
    kind               TEXT    NOT NULL
                       CHECK (kind IN ('decision','gotcha','convention','todo','fact')),
    title              TEXT    NOT NULL,
    body               TEXT    NOT NULL DEFAULT '',
    tags               TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    superseded_by      INTEGER REFERENCES notes(id) ON DELETE SET NULL,
    created_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
) STRICT;

-- Only live notes are worth indexing for the default query path.
CREATE INDEX IF NOT EXISTS idx_notes_live ON notes(project_id, kind) WHERE superseded_by IS NULL;

CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    title, body,
    content = 'notes',
    content_rowid = 'id',
    tokenize = "porter unicode61 tokenchars '_'"
);

CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;
CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.id, old.title, old.body);
END;
CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE OF title, body ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.id, old.title, old.body);
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;

-- -----------------------------------------------------------------------------
-- Housekeeping triggers
-- -----------------------------------------------------------------------------
-- Keep sessions.message_count honest without a COUNT(*) on every listing.
CREATE TRIGGER IF NOT EXISTS messages_count_ai AFTER INSERT ON messages BEGIN
    UPDATE sessions
       SET message_count = message_count + 1,
           updated_at    = strftime('%Y-%m-%dT%H:%M:%SZ','now')
     WHERE id = new.session_id;
END;
CREATE TRIGGER IF NOT EXISTS messages_count_ad AFTER DELETE ON messages BEGIN
    UPDATE sessions SET message_count = message_count - 1 WHERE id = old.session_id;
END;

-- -----------------------------------------------------------------------------
-- Views — the front-desk summaries
-- -----------------------------------------------------------------------------
-- What `recent_sessions` returns: the summary sheet, not the whole file.
-- Agents read this first and pull full transcripts only when needed
-- (progressive disclosure — protects their context window).
CREATE VIEW IF NOT EXISTS v_session_overview AS
SELECT s.id,
       s.agent,
       s.project_id,
       p.name        AS project,
       s.git_branch,
       s.title,
       s.summary,
       s.started_at,
       s.ended_at,
       s.message_count,
       (SELECT COUNT(DISTINCT ft.path) FROM file_touches ft
         WHERE ft.session_id = s.id AND ft.action <> 'read') AS files_changed,
       s.parent_session_id
  FROM sessions s
  LEFT JOIN projects p ON p.id = s.project_id;

INSERT OR IGNORE INTO schema_migrations(version, name) VALUES (1, 'core');
