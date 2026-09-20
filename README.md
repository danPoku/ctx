# ctx

ctx keeps one searchable copy of your coding-agent conversations, and lets any
agent search it.

I use Claude Code and Codex on the same projects. Each one writes its own
session logs, in its own format, and neither can see what the other worked out
yesterday. Neither is good at searching its own history either. ctx reads the
logs those tools already write, puts them in a single SQLite file, and answers
queries from a command line and from an MCP server, so an agent can ask "have
we dealt with this before?" and get an answer from any agent's past sessions.

It is a single Go binary. Everything stays on your machine.

```
$ ctx search "how does the daemon embed new chunks in the background"
[claude-code] 2026-09-20T16:11:47.479Z  claude-code:91ae043d-130e-4d25-8db5-266fe79bbbd5
    User: run the embed worker from the daemon  Assistant: Tests pass, inc…
```

## Status

Early. It has been run on one machine, on Ubuntu under WSL2;
other platforms are untested. There is no tagged release, and the database
schema and MCP tool output can still change. See
[Limitations](#limitations) before relying on it.

## What it does

- Reads Claude Code and Codex CLI session logs and stores them in
  `~/.ctx/ctx.db`.
- Splits each session into retrieval units: one user turn plus the assistant's
  reply, with tool noise trimmed.
- Searches them by keyword (SQLite FTS5) and, if you run a local embedding
  model, by meaning too. The two rankings are combined with reciprocal rank
  fusion.
- Groups sessions into projects by the normalised git remote of their working
  directory, so the same repository checked out in two places is one project.
- Serves the history over MCP, so Claude Code, Codex, or any MCP client can
  search it, page through a session, and save notes.
- Redacts API keys and similar secrets before anything is written to disk.
- Can run as a daemon that ingests sessions as they are written.

## Requirements

- Go 1.27 or newer, and a C compiler (the SQLite driver and the vendored
  sqlite-vec extension are built with cgo).
- The `sqlite_fts5` build tag. Every build and test command below includes it.
- Optional, for semantic search: [Ollama](https://ollama.com) with the
  `nomic-embed-text` model. Without it ctx falls back to keyword search and
  says so.

## Install

```
go install -tags sqlite_fts5 github.com/danPoku/ctx/cmd/ctx@latest
```

or from a clone:

```
git clone https://github.com/danPoku/ctx
cd ctx
go install -tags sqlite_fts5 ./cmd/ctx
```

Either puts `ctx` in `$(go env GOPATH)/bin`. Make sure that directory is on your
`PATH`.

## Quick start

```
ctx init                          # creates ~/.ctx and the database
ctx ingest                        # reads ~/.claude/projects and ~/.codex/sessions
ollama pull nomic-embed-text      # optional, enables semantic search
ctx embed                         # embeds chunks that don't have vectors yet
ctx search "refresh token race"   # searches the project you are standing in
```

`ctx search` is scoped to the project that contains your current directory. Use
`--agent claude-code` or `--agent codex` to restrict results to one agent, and
`--limit N` to change how many come back.

## Running it continuously

`ctx daemon` watches both session directories, ingests changes as they happen,
does a full rescan every five minutes in case a filesystem event was missed,
and embeds new chunks every 30 seconds if Ollama is reachable. If Ollama is
down it logs the error once and keeps ingesting.

As a systemd user service:

```
# ~/.config/systemd/user/ctx-daemon.service
[Unit]
Description=ctx daemon

[Service]
ExecStart=%h/go/bin/ctx daemon
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
```

```
systemctl --user enable --now ctx-daemon.service
```

After reinstalling the binary, run `systemctl --user restart ctx-daemon.service`
so the service picks it up. On WSL, user services stop when the WSL VM shuts
down and start again with your next session; anything written while it was off
is picked up by the next scan.

Daemon flags: `--embed-interval` (set to `0` to turn embedding off),
`--embed-batch`, `--ollama-url`, `--model`, `--fallback-interval`, `--debounce`.

## Connecting agents

`ctx mcp` speaks MCP over stdio. The agent starts it, and the server treats the
directory it was started in as the project.

Claude Code:

```
claude mcp add --scope user ctx -- ctx mcp
```

Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.ctx]
command = "ctx"
args = ["mcp"]
```

`codex exec` runs without a person to click "approve", so a tool call to ctx is
refused unless you also add `default_tools_approval_mode = "approve"` under that
same table. Interactive Codex asks instead.

Agents that were already running keep the old `ctx mcp` process until you start
a new chat.

### Tools

| Tool | What it does |
| --- | --- |
| `search_context` | Keyword plus semantic search over past sessions in this project, across all agents. Returns short snippets, each with the time of the turn it came from. |
| `get_session` | Reads a session's messages in order. Paged and capped at 200 messages per call. |
| `recent_sessions` | Lists recent sessions, newest first. |
| `sessions_touching` | Sessions that read or edited a given file. |
| `save_note` | Records a decision, gotcha, convention, todo, or fact. |
| `search_notes` | Searches saved notes. |

Search returns snippets on purpose. An agent that wants the full exchange calls
`get_session` with the session id and a sequence range.

## Commands

| Command | |
| --- | --- |
| `ctx init` | Create `~/.ctx` and the database. Other commands do this too if needed. |
| `ctx ingest [--path FILE] [--agent A]` | One-shot ingest of the default directories, or of a single file. |
| `ctx embed [--batch N]` | Embed pending chunks through Ollama. |
| `ctx search <query> [--agent A] [--limit N]` | Search the current project. |
| `ctx project list` | Show projects, their remotes, session counts and known paths. |
| `ctx project merge <from> <into>` | Fold one project into another. |
| `ctx mcp` | Serve MCP over stdio. |
| `ctx daemon` | Ingest and embed continuously. |

`ctx project merge` exists because a session whose working directory has since
been deleted or moved can no longer have its git remote looked up, so it lands
in a project of its own. Merging moves the sessions, paths, notes and file
touches, refuses to merge two projects that both have a remote, and queues the
moved chunks for re-embedding.

## How it works

**Ingest.** Each agent has an adapter that turns its log lines into a common
shape. For every log file ctx records how many bytes it has read and a hash of
the part it has already seen. Running ingest twice never duplicates a message,
a file that only grows is read from where it left off, and a file whose earlier
bytes changed is read again from the start with the old rows replaced. A line
that can't be parsed is recorded in `sources.last_error` and skipped; it never
stops the rest of the file.

**Chunks.** A chunk is one user turn and the assistant's reply. A turn is
indexed when the next user message arrives. The last turn of a session is
indexed once it ends in assistant text; bookkeeping events written after the
reply are ignored, and a turn that ends in a tool call is left until it
finishes.

**Search.** Keyword search is BM25 over an FTS5 index. Semantic search is a
cosine nearest-neighbour lookup in a sqlite-vec table, 768 dimensions. Results
from the two are merged by reciprocal rank fusion (k = 60). Vector neighbours
farther than a cosine distance of 0.50 are discarded, because the nearest
neighbour of an unrelated query is still "nearest". Keyword hits are kept
whatever their vector distance. If Ollama isn't reachable or nothing has been
embedded yet, search runs keyword-only and reports that it did.

**Projects.** The current directory is matched to a known project by longest
path prefix. A new directory inside a git repository with an `origin` remote
takes that remote, normalised to `host/owner/repo`, as its identity.

**Storage.** SQLite in WAL mode with strict tables. The schema is in
`migrations/`. The vector table is optional: if the extension fails to load,
everything else keeps working. The queries are in `queries/retrieval.sql`.

## Privacy

ctx only makes network requests to the Ollama URL you configure, which defaults
to `localhost`. Transcripts and embeddings never leave the machine.

Session logs contain whatever your agents saw, including the output of commands
they ran. Before storing a message, ctx redacts:

- well-known token formats (AWS access keys, GitHub, Slack, Google, Stripe and
  OpenAI/Anthropic-style keys, JWTs, `Bearer` tokens, PEM private keys),
- the value in `NAME=value` lines that look like `.env` entries,
- long tokens that mix letters and digits and have high entropy.

The original log line is kept in the `raw` column, redacted the same way.

This is pattern matching. It over-redacts on purpose (a UUID or commit hash can
be blanked out), and it will miss a secret that doesn't look like any of the
above. Treat `~/.ctx/ctx.db` as sensitive and don't share the file. ctx creates
`~/.ctx` with mode `0700` and tightens it if it finds it looser; leave it that
way.

## Limitations

- Only Claude Code and Codex CLI are supported. Adapters for other agents don't
  exist yet. Ingest has been checked against logs from Claude Code 2.1.278 and
  codex-cli 0.153.0. These formats are undocumented and change without notice,
  so a new version can break an adapter.
- ctx reads logs on the machine it runs on. Under WSL that means agents have to
  run inside WSL. Windows-native agents write to `C:\Users\…` and are not read.
- File-touch tracking (`sessions_touching`) only covers Claude Code's Read,
  Edit, MultiEdit and Write tools. Codex tool calls and edits made through a
  shell command are not recorded.
- The 0.50 distance cutoff was measured on about 60 chunks with
  `nomic-embed-text`. Expect to revisit it with a larger history or a different
  model.
- If a log file is rewritten so that it no longer mentions a session, that
  session's old rows stay in the database.
- There are no session summaries. The `summary` column exists and is unused.
- Redaction is best effort, as described above.

## Development

```
go vet -tags sqlite_fts5 ./...
go test -tags sqlite_fts5 ./...
```

`TestOllamaClientLive` talks to a real Ollama and needs `nomic-embed-text`
pulled; the rest of the suite runs without it.

Layout:

```
cmd/ctx/             the command line
internal/store/      database open, migrations, project resolution and merge
internal/ingest/     the ingest loop and chunker, plus claudecode/ and codex/ adapters
internal/redact/     secret redaction
internal/embed/      Ollama client and the embedding worker
internal/search/     keyword and hybrid queries
internal/mcpserver/  the MCP tools
internal/daemon/     filesystem watching
internal/vecext/     vendored sqlite-vec and its cgo registration
migrations/          SQL schema
queries/             named SQL queries
```

Each adapter has fixture files in its `testdata/` directory, taken from real
sessions. When a log format changes, capture a real sample and add it there
before changing the adapter.

`CLAUDE.md` is the project brief I give to the coding agents that work on this
repository. It records decisions that are already made, which is useful to
human contributors too.

## License

Copyright 2026 Dan Gyinaye Poku (dan.gyinaye@gmail.com)

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE). Some third-party code is vendored under `internal/vecext/`;
NOTICE lists it.
