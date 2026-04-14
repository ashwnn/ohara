# ohara - Implementation Specification

**Version:** 2.0
**Date:** April 13, 2026
**Status:** Ready for development
**Target:** Single developer handoff (offline-capable)

> Named after the island of scholars in One Piece - the library that held forbidden knowledge,
> because history shouldn't be erased.

### What changed from v1

v2 simplifies the architecture based on review of Engram (Gentleman-Programming/engram), Mem0, Supermemory, Letta, Databricks memory scaling research, and the broader AI agent memory landscape as of April 2026. Key changes:

- **Removed** automatic event capture (`event_log` table) and nightly LLM-powered distillation pipeline. Agent-curated saves are the only write path.
- **Removed** vector search (sqlite-vec, sqlite-lembed, Python sidecar) from launch scope. FTS5-only. Vector search becomes a future phase triggered by real retrieval failures, not speculation.
- **Added** contradiction detection on save (FTS5 similarity check for conflicting decisions).
- **Added** timeline browsing (`mem_timeline`) borrowed from Engram.
- **Added** `mem_update` tool for superseding/archiving outdated memories.
- **Added** BPE-accurate token counting (replacing `word_count * 1.3`).
- **Reduced** development from 4 phases to 2 shippable phases + 1 conditional future phase.

### Build vs. fork decision

Engram (Gentleman-Programming/engram) shares ~80% of ohara's architecture: Go + SQLite + FTS5 + agent-curated saves + OpenCode plugin hooks + same tool names + same save format. Engram is actively maintained (v1.12 beta, releases within the last week) and already runs on Arch Linux.

ohara's genuinely novel additions over Engram:

1. **Typed memory hierarchy** with enforced body limits per kind (10 kinds vs Engram's loose type strings)
2. **Global vs. project scope split** at the schema level
3. **Contradiction detection** on save (no equivalent in Engram or comparable tools)
4. **Memory versioning** via a revisions table with reason tracking
5. **Expiry/archive lifecycle** (discoveries expire at 90 days, postmortems at 30)

If those five features justify building from scratch rather than forking Engram, proceed with this spec. If not, fork Engram and add them. Both paths are valid.

**Naming note:** "Engram" in this spec always refers to the Gentleman-Programming/engram project - a Go binary persistent memory tool for AI coding agents. This is unrelated to DeepSeek's "Engram" paper ("Conditional Memory via Scalable Lookup: A New Axis of Sparsity for LLMs"), which is an internal LLM architecture module for N-gram hash lookups within the transformer. Different problems, different layers.

---

## 1. Overview

ohara is a local-first persistent memory system for OpenCode agent workflows. It gives AI coding agents durable, searchable memory that survives session restarts, context compaction, and process crashes.

**Core constraints:**

- Hardware: Lenovo ThinkCentre M910q, 8GB RAM, Arch Linux
- Runtime: 24/7 tmux host, multiple concurrent OpenCode instances
- Priority: Consistency and low resource usage over feature richness
- Write policy: Agent-curated saves only. The agent decides what's worth remembering. Shell history and git provide the raw audit trail.
- Search: FTS5 only at launch. Vector search deferred until real retrieval failures are observed.
- No MCP transport: Plugin-first integration, Go binary as headless backend

---

## 2. Architecture

```
OpenCode Instance A --+
OpenCode Instance B --+--> Each runs ohara.ts plugin
OpenCode Instance C --+         |
                                | HTTP (Unix socket)
                                v
                        ohara (Go binary)
                          |
                          +-- SQLite WAL + FTS5
                          +-- Contradiction detection on save
                          +-- Maintenance (backup, archive, integrity)
```

Two components. No more.

### 2.1 Go binary (ohara)

Headless storage and query engine. Single binary, zero runtime dependencies. Exposes an HTTP API over a Unix socket. Manages the SQLite database, handles search, and executes maintenance jobs.

**Why Go:** Engram already proves this pattern works. Single binary via `modernc.org/sqlite` (pure Go, no CGO). Tiny memory footprint. On 8GB RAM with multiple OpenCode instances, every MB matters.

**Why Unix socket over TCP:** No port conflicts. Multiple users or test instances can coexist. The socket file path is deterministic (`/run/user/$UID/ohara.sock`), so the plugin always knows where to connect. Falls back to `127.0.0.1:7331` if the socket path is unavailable.

### 2.2 OpenCode plugin (ohara.ts)

Thin TypeScript adapter (~250 lines). Hooks into OpenCode's lifecycle for injection and session management. Registers memory tools via the `tool()` helper so the agent can save, search, browse, and update memories. Communicates with the Go binary over HTTP.

**Why plugin tools instead of MCP:** The agent sees identical tool interfaces either way. Plugin tools avoid the MCP stdio transport layer, the known issue where MCP tool calls don't trigger `tool.execute.before`/`tool.execute.after` hooks, and the `-32601` noise from unsupported MCP methods (prompts, resources). If portability to other agents (Cursor, Claude Code) is needed later, an MCP stdio wrapper around the same HTTP API is a one-day addition, not a rearchitecture.

---

## 3. Memory Model

### 3.1 Hierarchy

Two scopes, strictly separated in the schema.

**Global scope** - shared across all projects, one namespace per user:

- `identity`: who the user is, communication preferences, reply style
- `user_preference`: toolchain choices, editor settings, language preferences
- `glossary`: terms, acronyms, naming conventions the user uses

**Project scope** - isolated per project root path:

- `decision`: architecture and design choices with rationale
- `pattern`: recurring fixes, gotchas, project-specific habits
- `bugfix`: what broke, why, how it was fixed, what to avoid
- `discovery`: things learned during exploration (API behavior, library quirks)
- `procedure`: step-by-step workflows ("how we deploy", "how we run migrations")
- `config`: environment setup, build flags, runtime requirements
- `postmortem`: session-level summaries of what was accomplished and what's pending

### 3.2 Memory lifecycle

```
Agent completes significant work
    |
    v
Agent calls mem_save (structured: title, kind, body, tags)
    |
    +-- Contradiction check (FTS5 similarity on title)
    |     +-- If near-duplicate found: return warning + existing ID
    |     +-- If no conflict: proceed
    |
    v
memory_items (curated, versioned, searchable)
    |
    +-- FTS5 indexed (title, body, tags)
    +-- Available via mem_search, mem_get, mem_timeline
    |
    v
Injection via plugin system.transform hook
```

This follows Engram's core philosophy: the agent already has the LLM, the full context, and understands what just happened. There is no separate compression pipeline, no automatic event firehose, no nightly distillation job. The agent's curated summaries are higher signal, more searchable, and don't bloat the database. Shell history and git provide the raw audit trail.

### 3.3 Memory kinds - field requirements

Every `memory_item` has a `kind` from the fixed enum above. Each kind enforces:

| Kind | Title format | Body max | Expiry | Auto-injectable |
|------|-------------|----------|--------|----------------|
| identity | Noun phrase | 500 chars | Never | Yes (global) |
| user_preference | "Prefers X over Y" | 300 chars | Never | Yes (global) |
| glossary | "Term: definition" | 200 chars | Never | Yes (global) |
| decision | "Chose X over Y for Z" | 1000 chars | Never (mark superseded) | Yes |
| pattern | "Verb + what" | 500 chars | Never | Yes |
| bugfix | "Fixed X in Y" | 1000 chars | Never | Yes |
| discovery | "Verb + what" | 500 chars | 90 days then archive | Yes |
| procedure | "How to X" | 2000 chars | Never | Yes |
| config | "X requires Y" | 500 chars | Never | Yes |
| postmortem | "Session: goal summary" | 2000 chars | 30 days then archive | No (on-demand only) |

"Archive" means the item stops appearing in automatic injection packs but remains searchable and retrievable via explicit `mem_search` or `mem_get`.

---

## 4. Database Schema

Single SQLite file: `~/.local/share/ohara/ohara.db`
WAL mode enabled at creation. Journal size limit 32MB.

### 4.1 Core tables

```sql
-- Curated memory items. Written by agent saves (mem_save, mem_session_summary).
CREATE TABLE memory_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    created_ts      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
    updated_ts      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
    project_id      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    kind            TEXT NOT NULL,
    scope           TEXT NOT NULL DEFAULT 'project',
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    tags            TEXT NOT NULL DEFAULT '[]',
    source          TEXT NOT NULL DEFAULT 'agent',
    status          TEXT NOT NULL DEFAULT 'active',
    superseded_by   INTEGER REFERENCES memory_items(id),
    expires_at      TEXT
);

CREATE INDEX idx_mem_project ON memory_items(project_id, status);
CREATE INDEX idx_mem_kind ON memory_items(kind, status);
CREATE INDEX idx_mem_scope ON memory_items(scope, status);
CREATE INDEX idx_mem_updated ON memory_items(updated_ts);


-- Version history for memory items. Append-only.
CREATE TABLE memory_revisions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id   INTEGER NOT NULL REFERENCES memory_items(id),
    ts          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
    actor_id    TEXT NOT NULL,
    field       TEXT NOT NULL,
    old_value   TEXT,
    new_value   TEXT,
    reason      TEXT
);

CREATE INDEX idx_rev_memory ON memory_revisions(memory_id, ts);


-- Session tracking
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    actor_id    TEXT NOT NULL DEFAULT 'agent',
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
    ended_at    TEXT,
    tool_count  INTEGER NOT NULL DEFAULT 0,
    prompt_count INTEGER NOT NULL DEFAULT 0,
    summary     TEXT,
    status      TEXT NOT NULL DEFAULT 'active'
);

CREATE INDEX idx_sess_project ON sessions(project_id, started_at);
```

### 4.2 Search indexes

```sql
-- FTS5 full-text search over memory items
CREATE VIRTUAL TABLE memory_fts USING fts5(
    title,
    body,
    tags,
    content='memory_items',
    content_rowid='id',
    tokenize='porter unicode61'
);

-- Triggers to keep FTS5 in sync
CREATE TRIGGER memory_fts_insert AFTER INSERT ON memory_items BEGIN
    INSERT INTO memory_fts(rowid, title, body, tags)
    VALUES (new.id, new.title, new.body, new.tags);
END;

CREATE TRIGGER memory_fts_update AFTER UPDATE ON memory_items BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, body, tags)
    VALUES ('delete', old.id, old.title, old.body, old.tags);
    INSERT INTO memory_fts(rowid, title, body, tags)
    VALUES (new.id, new.title, new.body, new.tags);
END;

CREATE TRIGGER memory_fts_delete AFTER DELETE ON memory_items BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, body, tags)
    VALUES ('delete', old.id, old.title, old.body, old.tags);
END;
```

**No vector search at launch.** FTS5 with BM25 scoring handles coding memory retrieval well because coding memories are keyword-dense: file names, function names, error messages, library names, config flags. If real retrieval failures are observed after extended use, sqlite-vec can be added later without schema changes (it uses a separate virtual table linked by memory_id). See Phase 3 and Appendix C.

---

## 5. Go Binary - API and Internals

### 5.1 HTTP API

All endpoints accept and return JSON. Served over Unix socket at `/run/user/$UID/ohara.sock`.

#### Health and admin

```
GET /health
  Response: { "status": "ok", "db_size_bytes": 12345, "memory_count": 42 }

GET /stats
  Response: { "memories": 42, "sessions": 15,
              "by_kind": {"decision": 10, "bugfix": 8, ...},
              "by_scope": {"global": 5, "project": 37},
              "db_size_bytes": 12345 }
```

#### Memory CRUD

```
POST /memories
  Body: { "project_id": "/home/user/myproject",
          "kind": "decision",
          "scope": "project",
          "title": "Chose SQLite over Postgres for local storage",
          "body": "What: Selected SQLite WAL...\nWhy: Single file, no daemon...",
          "tags": ["database", "architecture"],
          "source": "agent",
          "actor_id": "agent" }
  Response: { "id": 42 }
  Response (conflict): { "id": 42, "conflict": {
    "existing_id": 38, "existing_title": "Chose Postgres for storage",
    "similarity": 0.85, "message": "Similar decision found. Consider superseding #38." } }

GET /memories/:id
  Response: { full memory_item record }

PATCH /memories/:id
  Body: { "status": "superseded", "superseded_by": 43, "reason": "Updated approach" }
  Response: { "id": 42, "revision_id": 7 }
  Note: Creates a memory_revisions entry automatically.

DELETE /memories/:id
  Response: 405 Method Not Allowed
  Note: Memories are never deleted. Use PATCH to set status = 'archived'.
```

**Contradiction detection on POST /memories:** When a new memory is saved with kind `decision`, `pattern`, or `config`, the server runs an FTS5 query matching the new title against existing active items of the same kind in the same project. If a match exceeds the BM25 similarity threshold (configurable, default 0.7), the response includes a `conflict` field. The save still succeeds - it's a warning, not a block. The agent can then call PATCH to supersede the old item. This catches the most common form of memory drift: contradictory decisions that accumulate without any link between them.

#### Search

```
POST /search
  Body: { "query": "auth middleware jwt",
          "project_id": "/home/user/myproject",
          "scope": "all",
          "kinds": [],
          "status": "active",
          "limit": 10 }
  Response: { "results": [
    { "id": 42, "kind": "decision", "title": "...", "score": 0.87,
      "snippet": "first 150 chars...", "tags": [...], "updated_ts": "..." }
  ], "method": "fts5" }
```

Search execution order:

1. Run FTS5 BM25 query, get top 20 results with scores
2. Apply kind boosts: `decision` 1.2x, `bugfix` 1.1x, `pattern` 1.1x, everything else 1.0x
3. Apply recency boost: items updated within 7 days get 1.15x, within 30 days get 1.05x
4. Return top `limit` results

When vector search is added in Phase 3, step 1 becomes two parallel queries (FTS5 + sqlite-vec KNN) merged via Reciprocal Rank Fusion before applying boosts. See Appendix F.

#### Timeline (neighborhood browsing)

```
GET /memories/:id/timeline
  Query: ?count=3
  Response: { "anchor": { ...memory_item... },
              "before": [ ...up to count items before anchor by updated_ts... ],
              "after": [ ...up to count items after anchor by updated_ts... ] }
```

Returns the memories closest in time to the given item within the same project. Borrowed from Engram's `mem_timeline` tool. Lets the agent reconstruct narrative context ("what was I working on around the time I made this decision?") without dumping everything. Default count is 3 items before and 3 after.

#### Context pack (for system prompt injection)

```
POST /pack
  Body: { "project_id": "/home/user/myproject",
          "session_id": "ses_abc123",
          "budget_tokens": 400 }
  Response: { "pack": "<memory_context>\n## Identity\n...\n## Project\n...\n</memory_context>",
              "token_count": 380,
              "truncated": false,
              "item_count": 8 }
```

Pack assembly logic:

1. Always include: all `global` scope items with `status = 'active'` (identity, preferences, glossary). These are small by design (enforced body limits). If total global items exceed 150 tokens, truncate oldest preferences first.
2. Include: top 5 `project` scope items ranked by a combination of recency and retrieval score. If `session_id` is provided, boost items related to the current session's recent events.
3. Include: last session's postmortem for this project (if exists and < 30 days old), truncated to 100 tokens.
4. Format as structured XML block with section headers.
5. Hard-enforce `budget_tokens` ceiling. Count tokens using BPE tokenizer (see section 5.2). If over budget, drop items from the bottom of the project section first.

#### Session management

```
POST /sessions
  Body: { "id": "ses_abc123", "project_id": "/home/user/myproject", "actor_id": "agent" }
  Response: { "id": "ses_abc123", "created": true }
  Note: Idempotent. If session already exists, returns { "created": false }.

PATCH /sessions/:id
  Body: { "status": "completed", "summary": "...", "tool_count": 45 }

GET /sessions/:id/context
  Response: { "context": "Previous session summary + key decisions..." }
  Note: Returns context from the most recent completed session for the same project.
```

### 5.2 Token counting

The pack builder needs accurate token counts. The v1 spec used `word_count * 1.3`, which routinely overshoots or undershoots by 20-30%. On a 400-token budget, that means wasting 100 tokens of context or blowing past the limit.

Use `github.com/pkoukk/tiktoken-go` (pure Go port of OpenAI's tiktoken, supports cl100k_base). Accurate to within 1-2 tokens. Loads BPE ranks from a bundled file (~1.5MB). Called in two places: pack assembly and body limit enforcement on saves.

If the dependency feels heavy, a conservative `word_count * 1.35` with a 10% safety margin on the budget ceiling is an acceptable fallback.

### 5.3 Maintenance jobs

Runs as a systemd timer at 02:00 daily (configurable). The Go binary has a `ohara maintain` subcommand that:

1. **Archive**: Mark items past their `expires_at` as `status = 'archived'`.
2. **Integrity**: Run `PRAGMA integrity_check` (quick mode). Log result.
3. **Backup**: `sqlite3 .backup` to `~/.local/share/ohara/snapshots/ohara-YYYY-MM-DD.db`, then gzip. Retain last 7 daily snapshots.
4. **FTS5 optimize**: Run `INSERT INTO memory_fts(memory_fts) VALUES('optimize')` to merge FTS5 b-tree segments.

No LLM calls. No event processing. Pure database maintenance.

### 5.4 CLI subcommands

```
ohara serve                            # Start HTTP API (foreground, for systemd)
ohara serve --socket /path/to/sock     # Custom socket path
ohara serve --http 127.0.0.1:7331      # TCP fallback

ohara maintain                         # Run maintenance now
ohara maintain --dry-run               # Show what would be archived/backed up

ohara search "query"                   # Quick search from terminal
ohara stats                            # Print database statistics
ohara backup                           # Run backup now
ohara check                            # Run integrity checks
ohara version                          # Print version
```

### 5.5 Project structure

```
ohara/
  cmd/ohara/main.go              # CLI entrypoint, cobra commands
  internal/
    store/
      store.go                    # SQLite connection, migrations, WAL setup
      memories.go                 # memory_items CRUD, revisions
      sessions.go                 # session tracking
      search.go                   # FTS5 search with kind/recency boosts
      pack.go                     # context pack assembly
      timeline.go                 # timeline neighborhood queries
      conflict.go                 # contradiction detection on save
    server/
      server.go                   # HTTP API routes, Unix socket listener
      middleware.go               # request logging, error handling
    token/
      count.go                    # BPE token counting (tiktoken-go wrapper)
    maintain/
      maintain.go                 # archive, backup, integrity, FTS5 optimize
    config/
      config.go                   # JSONC config loading
  migrations/
    001_initial.sql
  go.mod
  go.sum
```

### 5.6 Dependencies (Go modules)

```
modernc.org/sqlite                          # Pure Go SQLite, no CGO
github.com/spf13/cobra                      # CLI framework
github.com/pkoukk/tiktoken-go              # BPE token counting (cl100k_base)
net/http (stdlib)                           # HTTP server
encoding/json (stdlib)                      # JSON handling
```

No web framework. stdlib `net/http` with a simple mux is sufficient for ~10 routes on a Unix socket.

---

## 6. OpenCode Plugin - Detailed Design

### 6.1 File location and registration

```
~/.config/opencode/plugins/ohara.ts
```

OpenCode auto-loads all `.ts` files from this directory at startup. No config entry needed for the plugin itself.

### 6.2 Plugin structure

```typescript
import type { Plugin } from "@opencode-ai/plugin"
import { tool } from "@opencode-ai/plugin"

const SOCKET_PATH = `/run/user/${process.getuid?.() ?? 1000}/ohara.sock`
const HTTP_FALLBACK = "http://127.0.0.1:7331"

// --- HTTP client to Go binary ---

async function oharaFetch(path: string, opts?: RequestInit): Promise<any> {
  const url = `http://unix:${SOCKET_PATH}:${path}`
  try {
    const res = await fetch(url, {
      ...opts,
      headers: { "Content-Type": "application/json", ...opts?.headers },
    })
    return res.ok ? res.json() : null
  } catch {
    const res = await fetch(`${HTTP_FALLBACK}${path}`, {
      ...opts,
      headers: { "Content-Type": "application/json", ...opts?.headers },
    })
    return res.ok ? res.json() : null
  }
}

function stripPrivate(text: string): string {
  return text.replace(/<private>[\s\S]*?<\/private>/gi, "[REDACTED]").trim()
}

// --- Session state ---

const sessionState = new Map<string, {
  projectId: string
  actorId: string
  toolCount: number
  promptCount: number
}>()

const subAgentSessions = new Set<string>()

async function ensureSession(sessionId: string, projectId: string) {
  if (sessionState.has(sessionId)) return
  if (subAgentSessions.has(sessionId)) return
  await oharaFetch("/sessions", {
    method: "POST",
    body: JSON.stringify({ id: sessionId, project_id: projectId, actor_id: "agent" }),
  })
  sessionState.set(sessionId, {
    projectId, actorId: "agent", toolCount: 0, promptCount: 0,
  })
}

// --- Auto-start ---

let startAttempted = false

async function ensureServer() {
  if (startAttempted) return
  startAttempted = true
  try {
    await oharaFetch("/health")
    return
  } catch {
    const { spawn } = await import("child_process")
    const proc = spawn("ohara", ["serve"], { detached: true, stdio: "ignore" })
    proc.unref()
    for (let i = 0; i < 20; i++) {
      await new Promise((r) => setTimeout(r, 100))
      try { await oharaFetch("/health"); return } catch {}
    }
    console.error("ohara: failed to start server")
  }
}

// --- Memory Protocol (static, injected every turn) ---

const MEMORY_PROTOCOL = `<memory_protocol>
You have persistent memory via mem_save, mem_search, mem_get, mem_timeline, mem_update, mem_context, mem_session_summary.
- Save proactively after significant work (decisions, bugfixes, patterns). Do not wait to be asked.
- Search before starting work on a topic to check for prior context.
- Use mem_timeline to browse what happened around a specific memory.
- If mem_save returns a conflict warning, review the existing memory and supersede it if appropriate.
- After compaction or context reset, call mem_context first.
- Before ending a long session, call mem_session_summary.
- Title format: "Verb + what" (e.g., "Fixed N+1 query in UserList")
- Body format: What / Why / Where / Learned
</memory_protocol>`

// --- Plugin export ---

export const OharaPlugin: Plugin = async (ctx) => {
  const projectId = ctx.directory
  await ensureServer()

  return {
    // === HOOK: System prompt injection ===
    "experimental.chat.system.transform": async (input, output) => {
      await ensureSession(input.sessionID, projectId)
      const pack = await oharaFetch("/pack", {
        method: "POST",
        body: JSON.stringify({
          project_id: projectId,
          session_id: input.sessionID,
          budget_tokens: 400,
        }),
      })
      if (pack?.pack) {
        output.system.push(pack.pack)
      }
      output.system.push(MEMORY_PROTOCOL)
    },

    // === HOOK: Compaction recovery ===
    "experimental.session.compacting": async (input, output) => {
      const prev = await oharaFetch(`/sessions/${input.sessionID}/context`)
      if (prev?.context) {
        output.context.push(prev.context)
      }

      output.context.push(
        [
          "CRITICAL: After compaction, your first action must be:",
          "1. Call mem_context to reload session state from persistent memory",
          "2. Call mem_search for any active task context",
          "Preserve in your summary: active decisions, unresolved bugs,",
          "exact file paths being worked on, and any user preferences stated this session.",
        ].join("\n")
      )
    },

    // === HOOK: Track prompt count for session stats ===
    "chat.message": async (input) => {
      await ensureSession(input.sessionID, projectId)
      const state = sessionState.get(input.sessionID)
      if (input.message?.role === "user" && state) {
        state.promptCount++
      }
    },

    // === HOOK: Track tool count for session stats ===
    "tool.execute.after": async (input) => {
      if (subAgentSessions.has(input.sessionID)) return
      const state = sessionState.get(input.sessionID)
      if (state) state.toolCount++
    },

    // === HOOK: Session lifecycle ===
    event: async (input) => {
      const evt = input.event
      if (evt.type === "session.created") {
        const sid = evt.properties?.info?.id ?? evt.properties?.id
        const parentId = evt.properties?.info?.parentID ?? evt.properties?.parentID
        if (sid && parentId) {
          subAgentSessions.add(sid)
          return
        }
        if (sid) await ensureSession(sid, projectId)
      }
      if (evt.type === "session.deleted") {
        const sid = evt.properties?.info?.id ?? evt.properties?.id
        if (sid && subAgentSessions.has(sid)) {
          subAgentSessions.delete(sid)
          return
        }
        if (sid) {
          const state = sessionState.get(sid)
          if (state) {
            await oharaFetch(`/sessions/${sid}`, {
              method: "PATCH",
              body: JSON.stringify({
                status: "completed",
                tool_count: state.toolCount,
                prompt_count: state.promptCount,
              }),
            })
            sessionState.delete(sid)
          }
        }
      }
    },

    // === TOOLS: Agent-facing memory operations ===
    tool: {
      mem_save: tool({
        description:
          "Save a structured memory. Use after significant work: decisions, bugfixes, " +
          "patterns, discoveries. Format: title as 'Verb + what', body as What/Why/Where/Learned.",
        args: {
          title: tool.schema.string().describe("Short searchable title: 'Fixed N+1 in UserList'"),
          kind: tool.schema
            .enum([
              "decision", "pattern", "bugfix", "discovery",
              "procedure", "config", "postmortem",
              "identity", "user_preference", "glossary",
            ])
            .describe("Memory type"),
          body: tool.schema.string().describe("Structured content: What/Why/Where/Learned"),
          tags: tool.schema.string().optional().describe("Comma-separated tags"),
        },
        async execute(args, context) {
          const scope = ["identity", "user_preference", "glossary"].includes(args.kind)
            ? "global" : "project"
          const result = await oharaFetch("/memories", {
            method: "POST",
            body: JSON.stringify({
              project_id: scope === "global" ? "__global__" : projectId,
              kind: args.kind, scope,
              title: args.title,
              body: stripPrivate(args.body),
              tags: args.tags?.split(",").map((t: string) => t.trim()) ?? [],
              source: "agent", actor_id: "agent",
            }),
          })
          if (!result) return "Failed to save memory. Check ohara service."
          let msg = `Saved memory #${result.id}: ${args.title}`
          if (result.conflict) {
            msg += `\n\nConflict detected: existing memory #${result.conflict.existing_id} ` +
              `"${result.conflict.existing_title}" (similarity: ${result.conflict.similarity.toFixed(2)}). ` +
              `Consider reviewing and superseding it via mem_update.`
          }
          return msg
        },
      }),

      mem_search: tool({
        description:
          "Search persistent memory. Returns compact results with IDs, titles, snippets, " +
          "and relevance scores. Use specific terms: file names, error messages, decision topics.",
        args: {
          query: tool.schema.string().describe("Search query"),
          scope: tool.schema.enum(["global", "project", "all"]).optional()
            .describe("Search scope (default: all)"),
          limit: tool.schema.number().optional().describe("Max results (default: 5)"),
        },
        async execute(args) {
          const result = await oharaFetch("/search", {
            method: "POST",
            body: JSON.stringify({
              query: args.query, project_id: projectId,
              scope: args.scope ?? "all", limit: args.limit ?? 5,
            }),
          })
          if (!result?.results?.length) return "No memories found."
          return result.results
            .map((r: any) =>
              `[#${r.id}] (${r.kind}) ${r.title} - ${r.snippet} [score: ${r.score.toFixed(2)}]`)
            .join("\n")
        },
      }),

      mem_get: tool({
        description: "Get the full content of a specific memory by ID.",
        args: { id: tool.schema.number().describe("Memory ID from search results") },
        async execute(args) {
          const result = await oharaFetch(`/memories/${args.id}`)
          if (!result) return "Memory not found."
          return [
            `# ${result.title}`,
            `Kind: ${result.kind} | Scope: ${result.scope} | Status: ${result.status}`,
            `Updated: ${result.updated_ts}`,
            `Tags: ${JSON.parse(result.tags).join(", ")}`,
            "", result.body,
          ].join("\n")
        },
      }),

      mem_timeline: tool({
        description:
          "Browse memories near a specific memory in time. Returns what was happening " +
          "before and after, within the same project. Use to reconstruct narrative context.",
        args: {
          id: tool.schema.number().describe("Memory ID to anchor the timeline"),
          count: tool.schema.number().optional().describe("Items before/after (default: 3)"),
        },
        async execute(args) {
          const result = await oharaFetch(
            `/memories/${args.id}/timeline?count=${args.count ?? 3}`)
          if (!result) return "Memory not found or no timeline available."
          const lines: string[] = []
          if (result.before?.length) {
            lines.push("--- Before ---")
            for (const m of result.before) {
              lines.push(`[#${m.id}] (${m.kind}) ${m.title} - ${m.updated_ts}`)
            }
          }
          lines.push(`--- Anchor: [#${result.anchor.id}] ${result.anchor.title} ---`)
          if (result.after?.length) {
            lines.push("--- After ---")
            for (const m of result.after) {
              lines.push(`[#${m.id}] (${m.kind}) ${m.title} - ${m.updated_ts}`)
            }
          }
          return lines.join("\n")
        },
      }),

      mem_update: tool({
        description:
          "Update an existing memory. Use to supersede outdated decisions, correct errors, " +
          "or archive memories that are no longer relevant.",
        args: {
          id: tool.schema.number().describe("Memory ID to update"),
          status: tool.schema.enum(["active", "archived", "superseded"]).optional()
            .describe("New status"),
          superseded_by: tool.schema.number().optional()
            .describe("ID of the memory that replaces this one"),
          body: tool.schema.string().optional().describe("Updated body content"),
          reason: tool.schema.string().optional().describe("Why this update was made"),
        },
        async execute(args) {
          const patch: Record<string, any> = {}
          if (args.status) patch.status = args.status
          if (args.superseded_by) patch.superseded_by = args.superseded_by
          if (args.body) patch.body = stripPrivate(args.body)
          if (args.reason) patch.reason = args.reason
          const result = await oharaFetch(`/memories/${args.id}`, {
            method: "PATCH",
            body: JSON.stringify(patch),
          })
          return result ? `Updated memory #${args.id} (revision #${result.revision_id})`
            : "Failed to update memory."
        },
      }),

      mem_context: tool({
        description:
          "Reload context from previous sessions. Call this after compaction or at session " +
          "start to recover state.",
        args: {},
        async execute() {
          const pack = await oharaFetch("/pack", {
            method: "POST",
            body: JSON.stringify({ project_id: projectId, budget_tokens: 600 }),
          })
          return pack?.pack ?? "No context available."
        },
      }),

      mem_session_summary: tool({
        description:
          "Save an end-of-session summary. Call before ending a long session. " +
          "Format: Goal, Discoveries, Accomplished, Pending, Relevant Files.",
        args: { summary: tool.schema.string().describe("Structured session summary") },
        async execute(args) {
          const result = await oharaFetch("/memories", {
            method: "POST",
            body: JSON.stringify({
              project_id: projectId, kind: "postmortem", scope: "project",
              title: `Session summary: ${new Date().toISOString().split("T")[0]}`,
              body: stripPrivate(args.summary),
              tags: ["session-summary"],
              source: "agent", actor_id: "agent",
            }),
          })
          return result ? `Session summary saved as memory #${result.id}`
            : "Failed to save session summary."
        },
      }),
    },
  }
}
```

---

## 7. Configuration

### 7.1 Go binary config

```jsonc
// ~/.config/ohara/config.jsonc
{
  "db_path": "~/.local/share/ohara/ohara.db",
  "socket_path": "/run/user/1000/ohara.sock",
  "http_addr": "127.0.0.1:7331",

  "search": {
    "kind_boosts": { "decision": 1.2, "bugfix": 1.1, "pattern": 1.1 },
    "recency_boost_7d": 1.15,
    "recency_boost_30d": 1.05
  },

  "conflict": {
    "enabled": true,
    "fts_threshold": 0.7,
    "check_kinds": ["decision", "pattern", "config"]
  },

  "pack": {
    "default_budget_tokens": 400,
    "max_budget_tokens": 800,
    "global_budget_tokens": 150,
    "postmortem_budget_tokens": 100,
    "max_project_items": 5
  },

  "timeline": {
    "default_count": 3,
    "max_count": 10
  },

  "maintain": {
    "schedule": "02:00"
  },

  "backup": {
    "snapshot_dir": "~/.local/share/ohara/snapshots",
    "retain_days": 7
  }
}
```

### 7.2 Directory layout

```
~/.config/ohara/
  config.jsonc

~/.local/share/ohara/
  ohara.db
  ohara.db-wal
  ohara.db-shm
  snapshots/
    ohara-2026-04-11.db.gz
  logs/
    ohara.log

~/.config/opencode/
  plugins/
    ohara.ts
```

---

## 8. Operations

### 8.1 systemd user service

```ini
# ~/.config/systemd/user/ohara.service
[Unit]
Description=ohara memory server
After=default.target

[Service]
Type=simple
ExecStart=%h/.local/bin/ohara serve
Restart=always
RestartSec=2
Nice=10
Environment=HOME=%h
MemoryMax=512M
MemoryHigh=256M

[Install]
WantedBy=default.target
```

### 8.2 Nightly maintenance timer

```ini
# ~/.config/systemd/user/ohara-maintain.timer
[Unit]
Description=ohara nightly maintenance

[Timer]
OnCalendar=*-*-* 02:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

```ini
# ~/.config/systemd/user/ohara-maintain.service
[Unit]
Description=ohara maintenance run

[Service]
Type=oneshot
ExecStart=%h/.local/bin/ohara maintain
Nice=15
IOSchedulingClass=idle
MemoryMax=256M
```

### 8.3 Installation sequence

```bash
# 1. Install Go binary
go install github.com/yourorg/ohara/cmd/ohara@latest

# 2. Initialize database
ohara serve &
sleep 1
ohara stats
kill %1

# 3. Install OpenCode plugin
cp plugin/opencode/ohara.ts ~/.config/opencode/plugins/ohara.ts

# 4. Enable systemd services
systemctl --user daemon-reload
systemctl --user enable --now ohara.service
systemctl --user enable --now ohara-maintain.timer

# 5. Verify
ohara stats
curl --unix-socket /run/user/$(id -u)/ohara.sock http://localhost/health
```

---

## 9. Development Phases

### Phase 1: Foundation (est. 1-2 weeks)

Deliverable: Go binary with SQLite + FTS5, HTTP API, plugin with hooks and all tools.

- [ ] Go project scaffold with cobra CLI
- [ ] SQLite store: migrations, WAL mode, memory_items, memory_revisions, sessions tables
- [ ] FTS5 virtual table with sync triggers
- [ ] BPE token counter integration (tiktoken-go)
- [ ] HTTP API over Unix socket: /health, /stats, /memories (CRUD), /search, /pack, /sessions, /memories/:id/timeline
- [ ] Contradiction detection on POST /memories
- [ ] Context pack assembly with BPE-accurate token budgeting
- [ ] OpenCode plugin: system.transform, compaction, session lifecycle hooks
- [ ] Plugin tools: mem_save, mem_search, mem_get, mem_timeline, mem_update, mem_context, mem_session_summary
- [ ] Auto-start server from plugin, sub-agent detection
- [ ] systemd service file
- [ ] Manual testing: start ohara, open OpenCode, verify injection, tools, timeline, conflict detection

**Exit criteria:** Agent can save, search, browse timeline, and update memories. System prompt includes memory pack. Compaction recovery works. Contradiction warnings fire on conflicting decisions. All data persists across OpenCode restarts.

### Phase 2: Hardening (est. 1 week)

Deliverable: Operational stability under multi-agent load.

- [ ] Maintenance job: archive expired, integrity check, backup, FTS5 optimize
- [ ] systemd timer for nightly maintenance
- [ ] Private tag stripping (double safety: plugin + binary)
- [ ] Token budget enforcement tests
- [ ] Concurrent multi-agent write testing (3 OpenCode instances)
- [ ] WAL checkpoint tuning (auto-checkpoint at 1000 pages)
- [ ] Memory limits testing on 8GB machine under load
- [ ] Backup restore drill
- [ ] Log rotation
- [ ] Docs: README, ARCHITECTURE.md, TROUBLESHOOTING.md

**Exit criteria:** Stable for 48+ hours under multi-agent load without memory growth, lock contention, or data loss. Maintenance runs clean.

### Phase 3: Vector search (future - only if needed)

**Trigger:** Real retrieval failures observed in practice where FTS5 keyword search misses relevant memories due to lexical mismatch. Do not start this phase preemptively.

Deliverable: sqlite-vec integration with hybrid search.

- [ ] sqlite-vec extension loading with graceful fallback (attempt load, set `vec_available` flag)
- [ ] Embedding generation via sqlite-lembed (GGUF) or ncruces WASM approach
- [ ] memory_vectors virtual table, population on insert/update
- [ ] Hybrid search: FTS5 + vec with RRF merge (see Appendix F)
- [ ] Embedding backfill CLI command for existing memories
- [ ] If sqlite-lembed doesn't work on Arch: evaluate ncruces/go-sqlite3 WASM bindings (see Appendix C)

**Exit criteria:** Semantic queries find relevant memories without exact keyword matches. FTS5-only fallback still works if extension fails.

---

## 10. Testing Strategy

### 10.1 Unit tests (Go)

- Store layer: CRUD operations, FTS5 queries, migration idempotency
- Search: FTS5 scoring, kind/recency boost math
- Pack: token budget enforcement (BPE-accurate), global vs project item selection
- Timeline: correct ordering, project scoping, count limits
- Conflict: FTS5 similarity detection, threshold behavior, response format
- Token counter: known inputs produce expected counts

### 10.2 Integration tests

- Full HTTP API round-trip: create memory, search, get, timeline, get pack
- Concurrent writes: 3 goroutines writing memories simultaneously
- Contradiction detection: save two conflicting decisions, verify conflict response
- Plugin to binary: mock OpenCode hooks, verify HTTP calls are correct

### 10.3 Functional recall tests

1. Save a decision about JWT vs session cookies. Search "authentication". Expect it to appear.
2. Save 3 bugfixes mentioning the same file. Search the filename. Expect all 3, recency-ranked.
3. Save a conflicting decision with similar title. Verify the conflict warning references the original.
4. Use mem_timeline on a memory. Verify before/after items are from same project, correctly ordered.
5. Restart OpenCode. New session. Verify system prompt contains memory pack.
6. Trigger compaction. Verify the new agent calls `mem_context` and recovers state.
7. Use mem_update to supersede a decision. Verify the old one stops appearing in pack injection.

### 10.4 Resource tests

- Load 500 memories. Measure resident memory (target: < 50MB).
- 100 search queries in a loop. p99 target: < 200ms.
- 3 OpenCode instances for 4 hours. Monitor ohara memory and CPU.

---

## 11. Open Questions for Developer

1. **Bun + Unix sockets:** OpenCode plugins run in Bun. Verify Bun's `fetch()` supports Unix sockets. If not, use TCP fallback as primary transport.

2. **Token counting trade-off:** tiktoken-go adds ~1.5MB to the binary and requires loading BPE rank data. If this is a problem, fall back to `word_count * 1.35` with 10% budget safety margin.

3. **FTS5 similarity threshold:** The 0.7 default for contradiction detection needs tuning in practice. Too low = noisy false positives. Too high = misses real conflicts. Make it configurable from day one.

4. **Memory body size limits:** The per-kind body limits (300-2000 chars) may need adjustment based on real agent behavior. Enforce with truncation + warning, not rejection.

---

## APPENDIX A: OpenCode Plugin API Reference

This section contains the complete plugin API documentation so the developer can work offline. Sourced from opencode.ai/docs/plugins/ and the Plugin API SDK docs as of April 2026.

### A.1 Plugin structure

A plugin is a TypeScript module that exports one or more plugin functions. Each function receives a context object and returns a hooks object.

```typescript
import type { Plugin } from "@opencode-ai/plugin"

export const MyPlugin: Plugin = async (ctx) => {
  // ctx provides:
  //   client: OpencodeClient   - SDK client instance
  //   project: Project          - Current project
  //   directory: string         - Project directory
  //   worktree: string          - Project worktree root
  //   serverUrl: URL            - Server URL
  //   $: BunShell               - Shell for running commands

  return {
    // hooks go here
  }
}
```

CRITICAL: The plugin function receives a context object, not individual parameters. Destructure what you need:

```typescript
// CORRECT
export const MyPlugin: Plugin = async ({ client, project, $, directory }) => { ... }

// WRONG
export const MyPlugin: Plugin = async (client) => { ... }
```

### A.2 Plugin loading

Two ways to load plugins:

1. **File-based:** Place .ts or .js files in the plugin directory:
   - Global: `~/.config/opencode/plugins/`
   - Project: `.opencode/plugin/`
   Files are automatically loaded at startup.

2. **npm packages:** Specify in config file:
   ```json
   { "plugin": ["my-plugin-package", "@my-org/custom-plugin"] }
   ```
   npm plugins are installed automatically using Bun at startup. Packages cached in `~/.cache/opencode/node_modules/`.

Load order: Local plugins first, then npm plugins. All hooks run in sequence. Duplicate npm packages with same name/version loaded once.

### A.3 Dependencies

Local plugins can use external npm packages. Add a `package.json` to your config directory:

```json
{
  "dependencies": {
    "some-package": "^1.0.0"
  }
}
```

OpenCode runs `bun install` at startup to install these.

### A.4 Hook reference

#### event

Subscribe to system events:

```typescript
event: async (input) => {
  // input.event.type can be:
  //   "session.created"    - new session started
  //   "session.deleted"    - session ended
  //   "message.created"    - new message
  //   "message.updated"    - message changed
  //   "message.deleted"    - message removed
  //
  // input.event.properties contains event-specific data
  //   For session events: properties.info.id, properties.info.parentID
}
```

#### chat.message

Fires on each chat message (after system.transform, before LLM call):

```typescript
"chat.message": async (input) => {
  // input.sessionID: string
  // input.message.role: "user" | "assistant"
  // input.message.parts: Array<{ text?: string, ... }>
}
```

#### chat.params

Modify LLM API call parameters:

```typescript
"chat.params": async (input, output) => {
  // output.temperature = 0.7
  // output.maxTokens = 4096
}
```

#### experimental.chat.system.transform

Fires before each LLM call. Use to inject into system prompt:

```typescript
"experimental.chat.system.transform": async (input, output) => {
  // input.sessionID: string
  // input.model: string
  // NOTE: Does NOT include user message text (known limitation, issue #17637)

  // Append to system prompt:
  output.system.push("Your injected context here")
}
```

IMPORTANT: `output.system` is an array. Use `.push()` to append.

#### experimental.chat.messages.transform

Transform messages before API call (invisible to UI):

```typescript
"experimental.chat.messages.transform": async (input, output) => {
  // input.messages: array of messages being sent to LLM
  // Modify output.messages to transform
}
```

#### experimental.session.compacting

Fires before LLM generates a continuation summary:

```typescript
"experimental.session.compacting": async (input, output) => {
  // Add context to preserve during compaction:
  output.context.push("Important state to preserve...")

  // Or replace the entire compaction prompt:
  output.prompt = "Custom compaction instructions..."
}
```

The default compaction prompt asks for: Goal, Instructions, Discoveries, Accomplished.

#### tool.execute.before / tool.execute.after

Fires before/after BUILT-IN tool execution:

```typescript
"tool.execute.after": async (input) => {
  // input.tool: string      - tool name ("edit", "write", "bash", "read", etc.)
  // input.args: object      - tool arguments
  // input.sessionID: string
  // input.output: string    - tool output (after only)
}
```

IMPORTANT: These hooks do NOT fire for MCP tool calls. Only built-in OpenCode tools trigger them. This is a known limitation (GitHub issue #2319).

#### tool (custom tools)

Register custom tools the agent can call:

```typescript
import { tool } from "@opencode-ai/plugin"

return {
  tool: {
    my_tool: tool({
      description: "What this tool does",
      args: {
        input: tool.schema.string().describe("Input text"),
        count: tool.schema.number().optional().describe("Optional count"),
      },
      async execute(args, context) {
        return "Tool result as string"
      },
    }),
  },
}
```

Schema types: `tool.schema.string()`, `tool.schema.number()`, `tool.schema.boolean()`, `tool.schema.enum([...])`. All support `.optional()` and `.describe()`.

#### permission hook

```typescript
permission: async (input) => {
  // input.tool: string
  // input.args: object
  // return "allow" | "deny" | undefined (use default)
}
```

### A.5 Logging

Use `client.app.log()` instead of `console.log` for structured logging:

```typescript
await client.app.log({
  body: {
    service: "ohara",
    level: "info",
    message: "Plugin initialized",
    extra: { foo: "bar" },
  },
})
```

### A.6 Compaction internals

- OpenCode reserves a safety buffer of 20,000 tokens (COMPACTION_BUFFER)
- Output tokens reserved: 32,000 (ProviderTransform.OUTPUT_TOKEN_MAX)
- Overflow check: `current_tokens > (context_limit - buffer)`
- When overflow detected, a CompactionPart is inserted into a new user message
- The compaction agent generates a summary in Goal/Instructions/Discoveries/Accomplished format
- Tool output pruning: old tool outputs are replaced with "[truncated]" before compaction
- Last 2 user turns are protected from pruning
- Tools like "skill" are never pruned

### A.7 Sub-agent detection

OpenCode spawns sub-agents via `Task()`. These create sessions with a `parentID` property. Detection pattern:

```typescript
if (evt.type === "session.created") {
  const parentId = evt.properties?.info?.parentID ?? evt.properties?.parentID
  if (parentId) {
    // Sub-agent session - skip
    return
  }
}
```

### A.8 Known OpenCode issues relevant to memory plugins

1. **RAM growth over time:** OpenCode's memory usage grows in long-running tmux sessions. The memory server must be independent of OpenCode's process.

2. **Orphaned `opencode attach` processes:** In tmux workflows, attach processes can orphan. Don't rely on OpenCode staying healthy.

3. **session.created not firing for plugins:** There are reported issues (GitHub #14808) where the session.created event doesn't reach plugins. The workaround is `ensureSession()` called from every hook - idempotent session creation that doesn't depend on the event.

4. **MCP -32601 errors:** When an MCP server doesn't support `prompts` or `resources` methods, OpenCode logs errors. These are harmless but noisy. Plugin tools avoid this entirely.

5. **Plugin never calls SDK APIs during initialization:** This avoids deadlock with the OpenCode HTTP server. SDK calls are safe inside hooks and pipeline methods, but NOT in the top-level plugin function.

---

## APPENDIX B: Engram Reference Architecture

Engram (Gentleman-Programming/engram) is the closest prior art. This section documents its architecture and key patterns so the developer can reference them without internet access.

### B.1 Overview

Engram is a single Go binary with SQLite + FTS5, exposed via CLI, HTTP API (port 7437), MCP server (stdio), and a TUI. Uses `modernc.org/sqlite` (pure Go, no CGO).

```
engram/
  cmd/engram/main.go          # CLI entrypoint
  internal/
    store/store.go             # Core: SQLite + FTS5 + all data operations
    server/server.go           # HTTP REST API server (port 7437)
    mcp/mcp.go                 # MCP stdio server (13 tools)
    sync/sync.go               # Git sync: manifest + chunks (gzipped JSONL)
    tui/                       # Interactive terminal UI (Bubble Tea)
  plugin/
    opencode/engram.ts         # OpenCode adapter plugin
```

### B.2 Memory philosophy

Agent-curated memory - the agent decides what to save. Raw tool calls are excluded because they're noisy and pollute FTS5 results. All memory comes from `mem_save` and `mem_session_summary`. Shell history and git provide the raw audit trail.

ohara follows this same philosophy (v2 removed the automatic event capture and distillation pipeline from v1).

### B.3 MCP tools (13 total)

```
mem_save              - Save structured observation
mem_search            - FTS5 search, returns compact results
mem_get_observation   - Get full untruncated content by ID
mem_timeline          - Chronological context around an observation
mem_context           - Recent context from previous sessions
mem_session_start     - Register start of new session
mem_session_end       - Mark session completed with optional summary
mem_session_summary   - Save comprehensive end-of-session summary
mem_update            - Update existing observation
mem_delete            - Delete observation
mem_stats             - Memory statistics
mem_health            - Health check
mem_list_sessions     - List recent sessions
```

### B.4 What ohara adds over Engram

| Feature | Engram | ohara |
|---------|--------|-------|
| Memory types | Loose strings (bugfix, decision, etc.) | 10-kind enum with enforced body limits |
| Scoping | Flat (all memories in one namespace) | Global vs project scope at schema level |
| Contradiction detection | None | FTS5 similarity check on save |
| Versioning | Overwrites in place | Revisions table with reason tracking |
| Expiry | Memories live forever | Per-kind expiry (discovery: 90d, postmortem: 30d) |
| Token counting | Not applicable (no pack builder) | BPE-accurate via tiktoken-go |
| MCP support | Primary transport | None (plugin-first, MCP wrapper possible later) |
| TUI | Yes (Bubble Tea) | No |
| Agent support | Any MCP agent | OpenCode only (at launch) |

### B.5 Setup on Arch Linux

```bash
brew install gentleman-programming/tap/engram

# Or from source:
git clone https://github.com/Gentleman-Programming/engram.git
cd engram
go install ./cmd/engram

# OpenCode setup:
engram setup opencode
```

---

## APPENDIX C: sqlite-vec Reference (Phase 3)

Retained from v1 for when vector search is needed. sqlite-vec is a vector search SQLite extension.

### C.1 Go bindings

Two options:

**Option 1: CGO-based (via mattn/go-sqlite3)**
```go
import (
    sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
    _ "github.com/mattn/go-sqlite3"
)
func init() { sqlite_vec.Auto() }
```

**Option 2: WASM-based (via ncruces/go-sqlite3) - NO CGO**
```go
import (
    _ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
    "github.com/ncruces/go-sqlite3"
)
```

The ncruces option embeds a custom WASM build of SQLite that includes sqlite-vec. No CGO. Recommended for ohara.

NOTE: `modernc.org/sqlite` (used in Phase 1-2) is NOT compatible with sqlite-vec CGO bindings. If adding vector search, either switch to ncruces/go-sqlite3 or load sqlite-vec as a dynamic extension.

### C.2 Schema

```sql
CREATE VIRTUAL TABLE memory_vectors USING vec0(
    memory_id INTEGER PRIMARY KEY,
    embedding FLOAT[384]
);
```

### C.3 KNN search

```sql
SELECT memory_id, distance
FROM memory_vectors
WHERE embedding MATCH ?
ORDER BY distance
LIMIT 20;
```

### C.4 Performance

384-dimensional float32 at 100k rows: KNN query p50 < 75ms. Brute-force (no ANN index). Memory-maps vector data.

---

## APPENDIX D: Codemem Reference

Codemem's durability and hybrid search patterns. ohara borrows its graceful degradation pattern (relevant for Phase 3).

### D.1 Architecture

- Storage: Single SQLite file with WAL mode
- Lexical: FTS5 with BM25
- Semantic: sqlite-vec `vec0` table storing 384-d float vectors
- Hybrid merge: reciprocal rank fusion with recency and kind boosts
- Graceful fallback: if sqlite-vec cannot load, lexical-only search still works

### D.2 Token-efficient retrieval

Progressive disclosure rather than dumping context:

1. `search(query)` returns only IDs + short abstracts (~50-100 tokens per result)
2. `timeline(anchor_id)` returns nearby events (~200-500 tokens per result)
3. `get_observations([ids])` returns full content (rare, on-demand)

ohara implements the same pattern: search returns snippets, `mem_get` returns full content, `mem_timeline` returns neighborhood, and the pack builder handles automatic injection with a hard token budget.

---

## APPENDIX E: Embedding Model Reference (Phase 3)

### E.1 all-MiniLM-L6-v2 (recommended for Phase 3)

- Dimensions: 384
- Parameters: 22.7M
- Max sequence length: 256 tokens
- Size on disk: ~80MB (PyTorch), ~45MB (GGUF f16)
- Speed: ~100-200 sentences/sec on CPU
- HuggingFace: `sentence-transformers/all-MiniLM-L6-v2`

This is the most commonly deployed model in the OpenCode memory ecosystem. Codemem, opencode-mem, innie, and macrodata all use 384-d models from this family.

### E.2 GGUF format

```bash
# Q8_0 quantization (~45MB)
curl -L https://huggingface.co/leliuga/all-MiniLM-L6-v2-GGUF/resolve/main/all-MiniLM-L6-v2.Q8_0.gguf \
  -o all-MiniLM-L6-v2.gguf
```

---

## APPENDIX F: Reciprocal Rank Fusion (Phase 3)

RRF merges FTS5 and vector search results. Retained for Phase 3.

### F.1 Algorithm

```
rrf_score(item) = sum over all lists L where item appears: 1 / (k + rank_in_L)
```

Where `k` = 60 (dampens contribution of low-ranked results).

### F.2 Go pseudocode

```go
func mergeRRF(ftsResults []SearchResult, vecResults []SearchResult, k int) []SearchResult {
    scores := make(map[int]float64)
    items := make(map[int]SearchResult)

    for rank, r := range ftsResults {
        scores[r.ID] += 1.0 / float64(k + rank + 1)
        items[r.ID] = r
    }
    for rank, r := range vecResults {
        scores[r.ID] += 1.0 / float64(k + rank + 1)
        if _, exists := items[r.ID]; !exists {
            items[r.ID] = r
        }
    }

    var merged []SearchResult
    for id, item := range items {
        item.Score = scores[id]
        merged = append(merged, item)
    }
    sort.Slice(merged, func(i, j int) bool {
        return merged[i].Score > merged[j].Score
    })
    return merged
}
```

### F.3 Post-RRF boosts

```go
for i := range merged {
    switch merged[i].Kind {
    case "decision":  merged[i].Score *= 1.2
    case "bugfix":    merged[i].Score *= 1.1
    case "pattern":   merged[i].Score *= 1.1
    }

    age := time.Since(merged[i].UpdatedAt)
    if age < 7 * 24 * time.Hour {
        merged[i].Score *= 1.15
    } else if age < 30 * 24 * time.Hour {
        merged[i].Score *= 1.05
    }
}
// Re-sort after boosts
```
