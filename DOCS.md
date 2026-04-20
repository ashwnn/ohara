[← Back to README](README.md)

# Ohara — Technical Reference

- [Database Schema](#database-schema)
- [HTTP API](#http-api-endpoints)
- [MCP Tools](#mcp-tools-31-tools)
- [Memory Protocol](#memory-protocol)
- [Project Name Normalization](#project-name-normalization)
- [Features](#features)
- [Design Decisions](#design-decisions)
- [Dependencies](#dependencies)

---

## Database Schema

### Tables

- **sessions** — `id` (TEXT PK), `project`, `directory`, `started_at`, `ended_at`, `summary`, `status`
- **memory_items** — `id` (INTEGER PK), `project_id`, `actor_id`, `kind`, `scope`, `title`, `body`, `tags`, `source`, `status`, `superseded_by`, `expires_at`, `domain`, `evidence_json`, `applies_to_json`, `related_json`, `classification`, `access_count`, `last_accessed`, `valid_from`, `valid_to`, `superseded_at`, `session_id`, `trust_level`, `ingested_at`, `written_by`, `trigger_condition`, `utility_weight`, `consolidated_from`
- **memory_relations** — `id` (INTEGER PK), `from_id`, `to_id`, `relation`, `created_at` — typed directional links between memories
- **memory_outcomes** — `id` (INTEGER PK), `memory_id`, `status`, `notes`, `actor_id`, `ts` — success/failure tracking per memory
- **memory_usage** — `id` (INTEGER PK), `memory_id`, `event`, `session_id`, `ts` — explicit usage events
- **memory_embeddings** — `memory_id` (PK), `embedding` (BLOB), `model`, `created_at` — float32 embedding vectors
- **entities** + **memory_entities** — entity graph for cross-memory entity queries
- **audit_log** — `id` (INTEGER PK), `memory_id`, `action`, `actor_id`, `session_id`, `trust_level`, `ts`, `snapshot` — append-only
- **user_prompts** + **prompts_fts** — user prompt storage with FTS5

### SQLite Configuration

- WAL mode for concurrent reads
- Busy timeout 5000ms
- Synchronous NORMAL
- Foreign keys ON
- Auto-checkpoint every 1000 WAL frames

---

## HTTP API Endpoints

All endpoints return JSON. Server listens on `127.0.0.1:7331`.

### Health

- `GET /health` — `{"status": "ok", "service": "ohara", "version": "<current>"}`

### Sessions

- `POST /sessions` — Create session
- `POST /sessions/{id}/end` — End session with summary
- `GET /sessions/recent` — Recent sessions

### Search

- `GET /search` — FTS5 search with type/project/scope/limit filters

### Timeline

- `GET /timeline` — Chronological context around a memory

### Prompts

- `POST /prompts` — Save user prompt
- `GET /prompts/recent` — Recent prompts
- `GET /prompts/search` — Search prompts

### Context

- `GET /context` — Formatted context pack

### Passive Capture

- `POST /mem/capture_passive` — Extract learnings from text

### Export / Import

- `GET /export` — Export all data as JSON
- `POST /import` — Import from JSON

### Stats

- `GET /stats` — Memory statistics

---

## MCP Tools (31 tools)

### Save & Update

| Tool | Purpose |
|------|---------|
| `mem_save` | Save structured observation with domain, classification, evidence, actor metadata |
| `mem_update` | Update observation by ID (partial update supported) |
| `mem_delete` | Soft or hard delete |
| `mem_forget` | Archive with documented reason, preserves audit trail |
| `mem_suggest_topic_key` | Stable topic key for upserts |

### Search & Retrieve

| Tool | Purpose |
|------|---------|
| `mem_search` | FTS5 + optional hybrid search with domain/classification/actor filters, relevance scoring |
| `mem_search_rerank` | Explicit slow-path LLM reranking (opt-in) |
| `mem_context` | Recent session context |
| `mem_prime` | Structured prime context with Knowledge vs Episode tier separation |
| `mem_pack` | Token-budgeted context pack |
| `mem_timeline` | Chronological context around a memory |
| `mem_graph_context` | Entity-centric graph traversal |

### Relations

| Tool | Purpose |
|------|---------|
| `mem_link` | Create typed relation (caused, resolves, supersedes, implements, contradicts) |
| `mem_unlink` | Remove a relation |
| `mem_related` | Traverse relations from a memory |

### Consolidation

| Tool | Purpose |
|------|---------|
| `mem_consolidate_candidates` | Grouped episodic memories ready for review |
| `mem_mark_consolidated` | Archive source memories after semantic consolidation |
| `mem_extract_entities` | Heuristic entity extraction and linking |

### Feedback & Outcomes

| Tool | Purpose |
|------|---------|
| `mem_mark_used` | Record usage event (increments access count) |
| `mem_append_outcome` | Append success/failure/unknown outcome |
| `mem_feedback` | Explicit utility feedback for RL-style weighting |

### Conflicts

| Tool | Purpose |
|------|---------|
| `mem_resolve_conflict` | Resolve via add/merge/invalidate/relate/suppress |

### Session Lifecycle

| Tool | Purpose |
|------|---------|
| `mem_session_start` | Register session start |
| `mem_session_end` | Mark session completed |
| `mem_session_summary` | Save structured end-of-session summary |

### Utilities

| Tool | Purpose |
|------|---------|
| `mem_save_prompt` | Save user prompt |
| `mem_capture_passive` | Extract learnings from text |
| `mem_stats` | System statistics |
| `mem_merge_projects` | Merge project name variants (admin) |
| `mem_list_domains` | List domains for a project (admin) |

---

## Memory Protocol

### WHEN TO SAVE

Call `mem_save` after:
- Bug fix completed
- Architecture or design decision made
- Non-obvious discovery
- Configuration change
- Pattern established
- User preference learned

Format:
- **title**: Verb + what — short, searchable
- **type**: `bugfix` | `decision` | `architecture` | `discovery` | `pattern` | `config` | `procedure` | `learning`
- **domain**: Subsystem scope (`auth`, `database`, `api`, etc.)
- **classification**: `foundational` | `tactical` | `observational`
- **content**: `**What**`, `**Why**`, `**Where**`, `**Learned**`

### WHEN TO SEARCH

1. `mem_context` — check recent session history
2. `mem_search` — FTS5 + optional hybrid search with domain filters

### SESSION CLOSE

Call `mem_session_summary` with structured summary:
- Goal, Instructions, Discoveries, Accomplished, Relevant Files

### AFTER COMPACTION

1. Call `mem_session_summary` with compacted summary
2. Call `mem_context` to recover previous context
3. Continue working

---

## Project Name Normalization

Ohara normalizes project names on write and read: lowercase, trimmed, collapsed hyphens/underscores. Auto-detection uses git remote, git root, or working directory basename.

---

## Features

### Full-Text Search (FTS5)

Searches across title, content, tool_name, type, and project. Query sanitization wraps words in quotes to avoid FTS5 syntax errors.

### Hybrid Retrieval

Opt-in FTS5 + Ollama embedding sidecar. Reciprocal Rank Fusion merges BM25 and cosine similarity scores. Zero LLM inference calls at query time — deterministic latency.

### Context Injection (`mem_prime`)

Token-budgeted markdown packs for direct system prompt injection. Two-tier model: Knowledge tier (decisions, patterns, procedures) included by default; Episode tier (raw session notes) opt-in only.

### Progressive Disclosure

Two-layer retrieval pattern:
1. `mem_search` — compact results with IDs
2. `mem_timeline` — chronological neighborhood

### Relation Graph

Six typed relations between memories: `caused`, `resolves`, `supersedes`, `related_to`, `implements`, `contradicts`. Enables traversal queries that flat search cannot answer.

### Conflict Detection

Save-time and retrieval-time contradiction surfacing. Five resolution actions: add, merge, invalidate, relate, suppress.

### Consolidation

Heuristic grouping of episodic memories into consolidation candidates. Background grouping, mandatory agent review before promotion to semantic knowledge.

### Secret Redaction

Regex-based pre-write redaction strips GitHub tokens, OpenAI keys, and other credential patterns before content touches the database.

### Privacy Tags

`<private>...</private>` content stripped at both plugin and store layers.

### Export / Import

JSON dump and restore of all sessions, memories, and prompts.

### No Raw Auto-Capture

All memory comes from the agent — curated summaries only.

---

## Design Decisions

1. **Go over TypeScript** — single binary, cross-platform, no runtime
2. **SQLite + FTS5 over vector DB** — FTS5 covers most use cases; embeddings are opt-in
3. **Agent-agnostic core** — Go binary is the brain, thin plugins per-agent
4. **Agent-driven compression** — the agent already has an LLM, no need for another
5. **Privacy at two layers** — strip in plugin AND store
6. **Pure Go SQLite** — no CGO, true cross-platform
7. **No raw auto-capture** — curated summaries only
8. **Zero LLM at retrieval time** — deterministic query latency, reranking is explicit opt-in

---

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/mark3labs/mcp-go` | v0.44.0 | MCP protocol implementation |
| `modernc.org/sqlite` | v1.45.0 | Pure Go SQLite driver (no CGO) |
