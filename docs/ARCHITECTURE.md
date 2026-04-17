[← Back to README](../README.md)

# Architecture

- [How It Works](#how-it-works)
- [Session Lifecycle](#session-lifecycle)
- [MCP Tools](#mcp-tools)
- [Progressive Disclosure](#progressive-disclosure-3-layer-pattern)
- [Memory Hygiene](#memory-hygiene)
- [Topic Key Workflow](#topic-key-workflow-recommended)
- [Project Structure](#project-structure)

---

## How It Works

Ohara trusts the **agent** to decide what's worth remembering — not a firehose of raw tool calls.

### The Agent Saves, Ohara Stores

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save with a structured summary:
   - title: "Fixed N+1 query in user list"
   - type: "bugfix"
   - content: What/Why/Where/Learned format
3. Ohara persists to SQLite with FTS5 indexing
4. Next session: agent searches memory, gets relevant context
```

---

## Session Lifecycle

```
Session starts → Agent works → Agent saves memories proactively
                                    ↓
Session ends → Agent writes session summary (Goal/Discoveries/Accomplished/Files)
                                    ↓
Next session starts → Previous session context is injected automatically
```

---

## MCP Tools

| Tool | Purpose |
|------|---------|
| `mem_save` | Save a structured observation (decision, bugfix, pattern, etc.) |
| `mem_update` | Update an existing observation by ID |
| `mem_delete` | Delete an observation (soft-delete by default, hard-delete optional) |
| `mem_forget` | Archive a memory with a documented reason; preserves audit trail |
| `mem_suggest_topic_key` | Suggest a stable `topic_key` for evolving topics before saving |
| `mem_search` | Full-text search across all memories with domain/classification/actor filters |
| `mem_search_rerank` | Optional slow-path LLM reranking on top of search results |
| `mem_context` | Get recent context from previous sessions |
| `mem_prime` | Build structured prime context with Knowledge vs Episode tier separation |
| `mem_pack` | Build a token-budgeted context pack from memory items |
| `mem_timeline` | Chronological context around a specific observation |
| `mem_get_observation` | Get full content of a specific memory |
| `mem_link` | Create a typed relation between two memories |
| `mem_unlink` | Remove a relation |
| `mem_related` | Traverse relations from a given memory |
| `mem_graph_context` | Entity-centric graph context retrieval |
| `mem_extract_entities` | Extract and link entities from a memory |
| `mem_consolidate_candidates` | Review grouped episodic memories for consolidation |
| `mem_mark_consolidated` | Archive source memories after semantic consolidation |
| `mem_mark_used` | Record that a memory was used (increments access count) |
| `mem_append_outcome` | Append success/failure outcome to a memory |
| `mem_feedback` | Record explicit utility feedback (RL-style weighting) |
| `mem_resolve_conflict` | Resolve a detected conflict via merge/link/invalidate/relate/suppress |
| `mem_session_summary` | Save end-of-session summary |
| `mem_session_start` | Register a session start |
| `mem_session_end` | Mark a session as completed |
| `mem_save_prompt` | Save a user prompt for future context |
| `mem_capture_passive` | Extract learnings from text output |
| `mem_stats` | Memory system statistics |
| `mem_merge_projects` | Merge project name variants into canonical name (admin) |
| `mem_list_domains` | List all distinct domains for a project (admin) |

---

## Progressive Disclosure (3-Layer Pattern)

Token-efficient memory retrieval — don't dump everything, drill in:

```
1. mem_search "auth middleware"     → compact results with IDs (~100 tokens each)
2. mem_timeline observation_id=42  → what happened before/after in that session
3. mem_get_observation id=42       → full untruncated content
```

---

## Memory Hygiene

- `mem_save` supports `scope` (`project` default, `personal` optional)
- `mem_save` also supports `topic_key`; with a topic key, saves become upserts
- Exact dedupe prevents repeated inserts in a rolling window
- Duplicates update metadata instead of creating new rows
- Topic upserts increment `revision_count`
- `mem_delete` uses soft-delete by default
- `mem_forget` archives with a documented reason and preserves the audit trail
- Search operations ignore soft-deleted and archived observations

---

## Topic Key Workflow (Recommended)

Use this when a topic evolves over time:

```text
1. mem_suggest_topic_key(type="architecture", title="Auth architecture")
2. mem_save(..., topic_key="architecture-auth-architecture")
3. Later change -> mem_save(..., same topic_key)
   => existing observation is updated (revision_count++)
```

`mem_suggest_topic_key` applies family heuristics:
- `architecture/*` for architecture/design changes
- `bug/*` for fixes, regressions, errors
- `decision/*`, `pattern/*`, `config/*`, `discovery/*`, `learning/*`

---

## Project Structure

```
ohara/
├── cmd/ohara/main.go               # CLI entrypoint
├── internal/
│   ├── store/                      # Core: SQLite + FTS5 + memory operations
│   │   ├── store.go                # Schema, migrations, session CRUD
│   │   ├── memories.go             # Memory CRUD, conflict detection, access tracking
│   │   ├── pack.go                 # Context pack and prime pack assembly
│   │   ├── hybrid.go               # FTS5 + embedding hybrid retrieval
│   │   └── graph_feedback.go       # Relation graph, entities, utility feedback
│   ├── server/server.go            # HTTP REST API
│   ├── mcp/mcp.go                  # MCP stdio server (31 tools)
│   ├── config/config.go            # Configuration loading
│   ├── redact/redact.go            # Secret redaction pipeline
│   ├── maintain/maintain.go        # Archive, backup, integrity
│   ├── setup/setup.go              # Agent plugin installer
│   └── sync/                       # Git sync (JSONL mirror)
├── plugin/                         # Agent plugins
└── go.mod / go.sum
```
