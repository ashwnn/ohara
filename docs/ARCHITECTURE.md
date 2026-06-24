# Architecture

[← Back to README](../README.md)

## How It Works

Ohara trusts the **agent** to decide what's worth remembering — not a firehose of raw tool calls.

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save with structured summary
3. Ohara persists to SQLite with FTS5 indexing
4. Next session: agent searches memory, gets relevant context
```

## Session Lifecycle

```
Session starts → Agent works → Agent saves memories proactively
                                    ↓
Session ends → Agent writes session summary (Goal/Discoveries/Accomplished/Files)
                                    ↓
Next session starts → Previous session context is injected automatically
```

## Project Structure

```
ohara/
├── cmd/ohara/main.go           # CLI entrypoint + all commands
├── internal/
│   ├── store/                  # Core: SQLite + FTS5 + memory operations
│   │   ├── store.go            # Schema, migrations, sessions, stats
│   │   ├── memories.go         # Memory CRUD, conflict detection, access tracking
│   │   ├── pack.go             # Context pack and prime pack assembly
│   │   ├── hybrid.go           # FTS5 + embedding hybrid retrieval
│   │   ├── ppr.go              # Personalized PageRank graph reranker
│   │   └── graph_feedback.go   # Relation graph, entities, utility feedback
│   ├── server/server.go        # HTTP REST API
│   ├── mcp/mcp.go              # MCP stdio server (35 tools)
│   ├── config/config.go        # Configuration loading
│   ├── redact/redact.go        # Secret redaction pipeline
│   ├── maintain/maintain.go    # Archive, backup, integrity
│   ├── setup/setup.go          # Agent plugin installer (all agents)
│   └── sync/                   # Git sync (JSONL mirror)
├── plugin/                     # Agent plugins
│   └── opencode/ohara.ts       # OpenCode native plugin
└── skills/                     # Agent instruction skills
```

## Progressive Disclosure

Token-efficient retrieval — don't dump everything, drill in:

1. `mem_search "auth middleware"` → compact results with IDs (~100 tokens each)
2. `mem_timeline memory_id=42` → what happened before/after in that session

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Go over TypeScript | Single binary, cross-platform, no runtime |
| SQLite + FTS5 over vector DB | FTS5 covers most use cases; embeddings opt-in |
| Agent-agnostic core | Go binary is brain, thin plugins per-agent |
| Agent-driven compression | The agent already has an LLM; no separate pipeline needed |
| Privacy at two layers | Strip in plugin AND store |
| Pure Go SQLite | No CGO, true cross-platform |
| No raw auto-capture | Curated summaries only |
| Zero LLM at retrieval time | Deterministic query latency; LLM reranking is explicit opt-in |

## Memory Hygiene

- Scope: `project` (default) or `personal`
- `topic_key`: enables upsert behavior (replaces on same key)
- Exact dedupe prevents repeated inserts in a rolling window
- `mem_forget` archives with documented reason (preserves audit trail)
- Archived/soft-deleted memories excluded from search by default
