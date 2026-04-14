[← Back to README](README.md)

# Ohara — Technical Reference

This is the complete technical reference for Ohara. For getting started, see the [README](README.md). For per-agent setup, see [Agent Setup](docs/AGENT-SETUP.md).

---

## Quick Navigation

| Section | What you'll find |
|---------|-----------------|
| [Database Schema](#database-schema) | Tables, FTS5, SQLite config |
| [HTTP API](#http-api-endpoints) | All REST endpoints |
| [MCP Tools](#mcp-tools-15-tools) | Detailed reference for all 15 memory tools |
| [Memory Protocol](#memory-protocol) | When/how agents should use the tools |
| [Project Name Normalization](#project-name-normalization) | Auto-detection and normalization |
| [Features](#features) | FTS5 search, timeline, privacy, export/import |
| [Running as a Service](#running-as-a-service) | systemd setup |
| [Design Decisions](#design-decisions) | Why Go, why SQLite, why no auto-capture |

For other docs:

| Doc | Description |
|-----|-------------|
| [Installation](docs/INSTALLATION.md) | Build from source only |
| [Agent Setup](docs/AGENT-SETUP.md) | Per-agent configuration |
| [Architecture](docs/ARCHITECTURE.md) | How it works, session lifecycle, CLI reference |

---

## Database Schema

### Tables

- **sessions** — `id` (TEXT PK), `project`, `directory`, `started_at`, `ended_at`, `summary`, `status`
- **observations** — `id` (INTEGER PK AUTOINCREMENT), `session_id` (FK), `type`, `title`, `content`, `tool_name`, `project`, `scope`, `topic_key`, `normalized_hash`, `revision_count`, `duplicate_count`, `last_seen_at`, `created_at`, `updated_at`, `deleted_at`
- **observations_fts** — FTS5 virtual table synced via triggers (`title`, `content`, `tool_name`, `type`, `project`)
- **user_prompts** — `id` (INTEGER PK AUTOINCREMENT), `session_id` (FK), `content`, `project`, `created_at`
- **prompts_fts** — FTS5 virtual table synced via triggers (`content`, `project`)

### SQLite Configuration

- WAL mode for concurrent reads
- Busy timeout 5000ms
- Synchronous NORMAL
- Foreign keys ON

---

## HTTP API Endpoints

All endpoints return JSON. Server listens on `127.0.0.1:7437`.

### Health

- `GET /health` — Returns `{"status": "ok", "service": "ohara", "version": "<current>"}`

### Sessions

- `POST /sessions` — Create session. Body: `{id, project, directory}`
- `POST /sessions/{id}/end` — End session. Body: `{summary}`
- `GET /sessions/recent` — Recent sessions. Query: `?project=X&limit=N`

### Observations

- `POST /observations` — Add observation. Body: `{session_id, type, title, content, tool_name?, project?, scope?, topic_key?}`
- `GET /observations/recent` — Recent observations. Query: `?project=X&scope=project|personal&limit=N`
- `GET /observations/{id}` — Get single observation by ID
- `PATCH /observations/{id}` — Update fields. Body: `{title?, content?, type?, project?, scope?, topic_key?}`
- `DELETE /observations/{id}` — Delete observation (`?hard=true` for hard delete)

### Search

- `GET /search` — FTS5 search. Query: `?q=QUERY&type=TYPE&project=PROJECT&scope=SCOPE&limit=N`

### Timeline

- `GET /timeline` — Chronological context. Query: `?observation_id=N&before=5&after=5`

### Prompts

- `POST /prompts` — Save user prompt. Body: `{session_id, content, project?}`
- `GET /prompts/recent` — Recent prompts. Query: `?project=X&limit=N`
- `GET /prompts/search` — Search prompts. Query: `?q=QUERY&project=X&limit=N`

### Context

- `GET /context` — Formatted context. Query: `?project=X&scope=project|personal`

### Passive Capture

- `POST /observations/passive` — Extract structured learnings from text. Body: `{content, session_id?, project?}`

### Export / Import

- `GET /export` — Export all data as JSON
- `POST /import` — Import data from JSON. Body: ExportData JSON

### Stats

- `GET /stats` — Memory statistics

### Project Migration

- `POST /projects/migrate` — Migrate observations between project names. Body: `{source, target}`

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `OHARA_DATA_DIR` | Override data directory | `~/.ohara` |
| `OHARA_PORT` | Override HTTP server port | `7437` |
| `OHARA_PROJECT` | Override project name for MCP server | auto-detected via git |

---

## MCP Tools (15 tools)

### mem_search

Search persistent memory across all sessions. Supports FTS5 full-text search with type/project/scope/limit filters.

### mem_save

Save structured observations:

- **title**: Short, searchable (e.g. "JWT auth middleware")
- **type**: `decision` | `architecture` | `bugfix` | `pattern` | `config` | `discovery` | `learning`
- **scope**: `project` (default) | `personal`
- **topic_key**: optional canonical topic id (e.g. `architecture/auth-model`)
- **content**: Structured with `**What**`, `**Why**`, `**Where**`, `**Learned**`

Exact duplicate saves are deduplicated using normalized content hash + project + scope + type + title.

### mem_update

Update an observation by ID. Supports partial updates for `title`, `content`, `type`, `project`, `scope`, and `topic_key`.

### mem_suggest_topic_key

Suggest a stable `topic_key` from `type + title`. Uses family heuristics like `architecture/*`, `bug/*`, etc.

### mem_delete

Delete an observation by ID. Uses soft-delete by default.

### mem_save_prompt

Save user prompts — records what the user asked.

### mem_context

Get recent memory context from previous sessions.

### mem_stats

Show memory system statistics.

### mem_timeline

Chronological context around a specific observation.

### mem_get_observation

Get full untruncated content of a specific observation by ID.

### mem_session_summary

Save comprehensive end-of-session summary:

```
## Goal
## Instructions
## Discoveries
## Accomplished
## Relevant Files
```

### mem_session_start

Register the start of a new coding session.

### mem_session_end

Mark a session as completed with optional summary.

### mem_capture_passive

Extract structured learnings from text output. Looks for `## Key Learnings:` sections.

### mem_merge_projects

**Admin tool.** Merge multiple project name variants into a single canonical name.

---

## Memory Protocol

The Memory Protocol teaches agents **when** and **how** to use Ohara's MCP tools.

### WHEN TO SAVE (mandatory)

Call `mem_save` IMMEDIATELY after:
- Bug fix completed
- Architecture or design decision made
- Non-obvious discovery
- Configuration change
- Pattern established
- User preference learned

Format for `mem_save`:
- **title**: Verb + what — short, searchable
- **type**: `bugfix` | `decision` | `architecture` | `discovery` | `pattern` | `config` | `preference`
- **scope**: `project` (default) | `personal`
- **topic_key** (optional): stable key like `architecture/auth-model`
- **content**:
  ```
  **What**: One sentence
  **Why**: What motivated it
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases
  ```

### Topic update rules (mandatory)

- Different topics must not overwrite each other
- Reuse the same `topic_key` to update evolving topics
- Call `mem_suggest_topic_key` first if unsure

### WHEN TO SEARCH MEMORY

When the user asks to recall something:
1. First call `mem_context`
2. If not found, call `mem_search`
3. Use `mem_get_observation` for full content

Also search proactively when starting work that might have been done before.

### SESSION CLOSE PROTOCOL (mandatory)

Before ending a session, call `mem_session_summary` with:

```
## Goal
[What we were working on]

## Instructions
[User preferences discovered]

## Discoveries
- [Technical findings]

## Accomplished
- [Completed items]

## Next Steps
- [What remains]

## Relevant Files
- path/to/file — [what changed]
```

### AFTER COMPACTION

If compaction/context reset occurs:
1. Call `mem_session_summary` with compacted summary
2. Call `mem_context` to recover previous context
3. Only THEN continue working

---

## Project Name Normalization

Ohara prevents project name drift by normalizing on write and read: **lowercase**, **trimmed**, **collapsed hyphens/underscores**.

### Auto-detection

MCP server auto-detects project name:
1. `--project` flag
2. `OHARA_PROJECT` environment variable
3. Git remote origin URL
4. Git repository root directory name
5. Current working directory basename

### Similar-project warnings

When saving to a new project, Ohara checks for similar existing names and warns if a variant exists.

---

## Features

### Full-Text Search (FTS5)

- Searches across title, content, tool_name, type, and project
- Query sanitization: wraps words in quotes to avoid FTS5 syntax errors

### Timeline (Progressive Disclosure)

Three-layer pattern:

1. `mem_search` — Find relevant observations
2. `mem_timeline` — Drill into chronological neighborhood
3. `mem_get_observation` — Get full content

### Privacy Tags

`<private>...</private>` content is stripped:

- Example: `Set up API with <private>sk-abc123</private>` becomes `Set up API with [REDACTED]`

### User Prompt Storage

Separate table captures what the USER asked. Full FTS5 search support.

### Export / Import

- `ohara export` — JSON dump of all sessions, observations, prompts
- `ohara import <file>` — Load from JSON with duplicate handling

### No Raw Auto-Capture

All memory comes from the agent — no firehose of raw tool calls. The agent's curated summaries are higher signal and more searchable.

---

## Running as a Service

### Using systemd

1. Move binary to `~/.local/bin` (ensure in `$PATH`)
2. Create directories: `mkdir -p ~/.ohara ~/.config/systemd/user`
3. Create `~/.config/systemd/user/ohara.service`:

```ini
[Unit]
Description=Ohara Memory Server
After=network.target

[Service]
WorkingDirectory=%h
ExecStart=%h/.local/bin/ohara serve
Restart=always
RestartSec=3
Environment=OHARA_DATA_DIR=%h/.ohara

[Install]
WantedBy=default.target
```

4. `systemctl --user daemon-reload`
5. `systemctl --user enable ohara`
6. `systemctl --user start ohara`
7. `journalctl --user -u ohara -f`

---

## Design Decisions

1. **Go over TypeScript** — Single binary, cross-platform, no runtime
2. **SQLite + FTS5 over vector DB** — FTS5 covers 95% of use cases
3. **Agent-agnostic core** — Go binary is the brain, thin plugins per-agent
4. **Agent-driven compression** — The agent already has an LLM
5. **Privacy at two layers** — Strip in plugin AND store
6. **Pure Go SQLite** — No CGO means true cross-platform distribution
7. **No raw auto-capture** — Curated summaries only

---

## Dependencies

### Go

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/mark3labs/mcp-go` | v0.44.0 | MCP protocol implementation |
| `modernc.org/sqlite` | v1.45.0 | Pure Go SQLite driver |


