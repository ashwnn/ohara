[← Back to README](README.md)

# Documentation

- [Database Schema](#database-schema)
- [HTTP API Endpoints](#http-api-endpoints)
- [MCP Tools](#mcp-tools-33-tools)
- [Design Decisions](#design-decisions)

## Core Entry Points

- [docs/INSTALLATION.md](docs/INSTALLATION.md) — install and source-build entry
- [docs/OPERATIONS.md](docs/OPERATIONS.md) — run, validate, back up, and repair
- [docs/PRODUCTION_NOTES.md](docs/PRODUCTION_NOTES.md) — tested scope and known
  limits
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution workflow
- [SECURITY.md](SECURITY.md) — vulnerability reporting
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — contributor standards

## Agent Integration

- **sessions** — `id` (TEXT PK), `project`, `directory`, `started_at`, `ended_at`, `summary`, `status`
- **memory_items** — `id` (INTEGER PK), `project_id`, `actor_id`, `kind`, `scope`, `title`, `body`, `tags`, `source`, `status`, `superseded_by`, `expires_at`, `domain`, `evidence_json`, `applies_to_json`, `related_json`, `classification`, `access_count`, `last_accessed`, `session_id`, `written_by`, `trigger_condition`, `utility_weight`, `consolidated_from`, `idempotency_key`
- **memory_relations** — typed directional links: `from_id`, `to_id`, `relation` (caused, resolves, supersedes, implements, contradicts)
- **memory_outcomes** — success/failure tracking per memory
- **memory_usage** — explicit usage events
- **memory_embeddings** — float32 embedding vectors (opt-in Ollama sidecar)
- **entities** + **memory_entities** — entity graph for cross-memory queries
- **audit_log** — append-only, snapshots before mutation
- **user_prompts** + **prompts_fts** — user prompt storage with FTS5

### SQLite Config

WAL mode, busy timeout 5000ms, synchronous NORMAL, foreign keys ON.

- OpenCode: `ohara setup opencode`
- Other agents: `ohara mcp` for stdio, or remote MCP over HTTP when network
  transport is required

The MCP server supports these profiles:

Base URL: `http://127.0.0.1:7331`. All JSON.

### Stability Rules

- Routes listed here are the supported contract. Legacy aliases may exist; new integrations should use canonical routes.
- Error responses use non-2xx HTTP status codes with JSON or text error body.
- Breaking route or payload changes require docs and tests in the same change.
- The API assumes a trusted local caller. Do not expose directly to a network. For remote MCP, use the dedicated remote MCP mode.

### Endpoints

| Route | Method | Purpose |
|-------|--------|---------|
| `/health` | GET | Service status, version, DB size |
| `/sessions` | POST | Create session |
| `/sessions/{id}/end` | POST | End session with summary |
| `/sessions/recent` | GET | Recent sessions |
| `/search` | GET | FTS5 search with filters |
| `/timeline` | GET | Chronological context around a memory |
| `/prompts` | POST | Save user prompt |
| `/prompts/recent` | GET | Recent prompts |
| `/prompts/search` | GET | Search prompts |
| `/context` | GET | Formatted context pack |
| `/mem/capture_passive` | POST | Extract learnings from text |
| `/export` | GET | Export all data as JSON |
| `/import` | POST | Import from JSON |
| `/stats` | GET | Memory statistics |
| `/observe` | POST | Store raw observation (plugin support) |
| `/memories` | GET/POST | List/create memory items |
| `/memories/search` | GET | Memory search |
| `/memories/{id}` | GET/PATCH/DELETE | Single memory CRUD |
| `/memories/{id}/timeline` | GET | Per-memory timeline |
| `/pack` | POST | Build context pack |
| `/projects/migrate` | POST | Merge memories from old project name to canonical |
| `/sync/status` | GET | Autosync phase, last error, backoff |
| `/files/history` | GET | File-scoped memory history |
| `/files/context` | POST | File-focused context pack |

For remote MCP mode (streamable HTTP at `/mcp`), see the Operations runbook.

Memory retrieval:

## MCP Tools (33 tools)

Memory mutation:

| Tool | Purpose |
|------|---------|
| `mem_save` | Save structured observation (domain, classification, evidence, actor) |
| `mem_update` | Update by ID (partial) |
| `mem_delete` | Soft or hard delete |
| `mem_forget` | Archive with documented reason |
| `mem_suggest_topic_key` | Stable upsert key |

Remote MCP:

| Tool | Purpose |
|------|---------|
| `mem_search` | FTS5 + optional hybrid, domain/kind/actor filters, relevance scoring |
| `mem_search_rerank` | Explicit LLM reranking (opt-in) |
| `mem_context` | Recent session context |
| `mem_prime` | Knowledge vs Episode tier markdown packs |
| `mem_pack` | Token-budgeted context pack |
| `mem_pack_explain` | Per-memory score breakdown |
| `mem_timeline` | Chronological context around a memory |
| `mem_graph_context` | Entity-centric traversal |
| `mem_file_history` | Recent memories for a file path |
| `mem_file_context` | File-focused context pack |

Use `streamable-http` as the canonical remote transport. Keep auth enabled if
you expose remote MCP.

| Tool | Purpose |
|------|---------|
| `mem_link` | Create typed relation (caused, resolves, supersedes, implements, contradicts) |
| `mem_unlink` | Remove relation |
| `mem_related` | Traverse relations |

Ohara is a local-first memory layer for coding agents. The boundaries are:

| Tool | Purpose |
|------|---------|
| `mem_consolidate_candidates` | Grouped episodic memories for review |
| `mem_mark_consolidated` | Archive sources after semantic consolidation |
| `mem_extract_entities` | Heuristic entity extraction and linking |

Runtime shape:

| Tool | Purpose |
|------|---------|
| `mem_mark_used` | Record usage (increments access_count) |
| `mem_append_outcome` | Append success/failure outcome |
| `mem_feedback` | Explicit utility feedback (RL weighting) |

Storage and retrieval:

- canonical data lives in SQLite
- full-text retrieval uses FTS5
- derived work such as embeddings or relation extraction runs through durable
  SQLite-backed jobs after writes commit
- hybrid retrieval can add vector ranking when embeddings are available

Integration boundaries:

- plugin behavior in `plugin/opencode`
- MCP handlers in `internal/mcp`
- HTTP handlers in `internal/server`
- persistence and retrieval logic in `internal/store`

Design rules:

- the agent decides what is worth remembering
- Ohara stores curated memories, not a full raw transcript pipeline
- local-first defaults beat network-first complexity

## Comparison

Compared with `claude-mem`, Ohara is optimized for:

- one Go binary instead of a multi-runtime stack
- OpenCode plus generic MCP agent support
- curated memory instead of raw transcript capture
- SQLite and FTS5 first, with optional hybrid retrieval on top

Choose `claude-mem` if you specifically want transcript-style capture inside the
Claude Code ecosystem.

## Benchmarks

Run benchmarks when changing retrieval quality, storage behavior, token
counting, or memory lifecycle logic.

Core commands:

```bash
go test ./bench/quality/... -v -bench=. -benchtime=1s
go test ./bench/store/ -bench=. -benchmem -benchtime=1s
go test ./bench/forgetting/ -v
go run ./bench/precision/ -k 3
go test ./bench/retrieval/ -v
go run ./bench/run_retrieval.go -k 5
go test ./bench/longmemeval/ -v
go run ./bench/run_longmemeval.go -k 5
go test ./internal/token/ -bench=. -benchmem -benchtime=1s
```

Useful retrieval variants:

| Tool | Purpose |
|------|---------|
| `mem_session_start` | Register session start |
| `mem_session_end` | Mark session completed |
| `mem_session_summary` | Save end-of-session summary |

## Documentation Rules

| Tool | Purpose |
|------|---------|
| `mem_save_prompt` | Save user prompt |
| `mem_capture_passive` | Extract learnings from text |
| `mem_stats` | System statistics |
| `mem_merge_projects` | Merge project name variants (admin) |
| `mem_list_domains` | List domains for a project (admin) |

### Tool Profiles

The MCP server supports profiles to control visible tools:

```bash
ohara mcp                       # All 33 tools (default)
ohara mcp --tools=agent         # 26 tools agents use in practice
ohara mcp --tools=admin         # 5 tools for manual curation
ohara mcp --tools=agent,admin   # Combine profiles
ohara mcp --tools=mem_save,mem_search  # Specific tool names
```

---

## Design Decisions

1. **Go over TypeScript** — single binary, no runtime
2. **SQLite + FTS5 over vector DB** — FTS5 covers most use cases; embeddings opt-in
3. **Agent-agnostic core** — Go binary is brain, thin plugins per-agent
4. **Agent-driven compression** — agent already has an LLM, no need for another
5. **Privacy at two layers** — strip in plugin AND store
6. **Pure Go SQLite** — no CGO, true cross-platform
7. **No raw auto-capture** — curated summaries only
8. **Zero LLM at retrieval time** — deterministic query latency, reranking is explicit opt-in

---

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/mark3labs/mcp-go` | v0.44.0 | MCP protocol |
| `modernc.org/sqlite` | v1.45.0 | Pure Go SQLite (no CGO) |
