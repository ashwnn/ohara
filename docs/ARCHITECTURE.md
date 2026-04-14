[← Back to README](../README.md)

# Architecture

- [How It Works](#how-it-works)
- [Session Lifecycle](#session-lifecycle)
- [MCP Tools](#mcp-tools)
- [Progressive Disclosure](#progressive-disclosure-3-layer-pattern)
- [Memory Hygiene](#memory-hygiene)
- [Topic Key Workflow](#topic-key-workflow-recommended)
- [Project Structure](#project-structure)
- [CLI Reference](#cli-reference)

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
| `mem_suggest_topic_key` | Suggest a stable `topic_key` for evolving topics before saving |
| `mem_search` | Full-text search across all memories |
| `mem_session_summary` | Save end-of-session summary |
| `mem_context` | Get recent context from previous sessions |
| `mem_timeline` | Chronological context around a specific observation |
| `mem_get_observation` | Get full content of a specific memory |
| `mem_save_prompt` | Save a user prompt for future context |
| `mem_stats` | Memory system statistics |
| `mem_session_start` | Register a session start |
| `mem_session_end` | Mark a session as completed |
| `mem_capture_passive` | Extract learnings from text output |
| `mem_merge_projects` | Merge project name variants into canonical name (admin) |

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
- Search operations ignore soft-deleted observations

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
├── cmd/ohara/main.go               # CLI entrypoint (binary: ohara)
├── internal/
│   ├── store/store.go              # Core: SQLite + FTS5 + data ops
│   ├── server/server.go            # HTTP REST API (port 7437)
│   ├── mcp/mcp.go                  # MCP stdio server (15 tools)
│   ├── setup/setup.go              # Agent plugin installer
│   └── project/                    # Project name detection
├── plugin/                         # Agent plugins (OpenCode, etc.)
├── skills/                         # AI skills and guardrails
├── DOCS.md                         # Full technical documentation
└── go.mod / go.sum
```

**Note:** This fork focuses on MCP and HTTP API. TUI and sync features from upstream are not included.

---

## CLI Reference

```
ohara setup [agent]       Install/setup agent integration
ohara serve [port]        Start HTTP API server (default: 7437)
ohara mcp                 Start MCP server (stdio transport)
ohara search <query>      Search memories
ohara save <title> <msg>  Save a memory
ohara timeline <obs_id>   Chronological context around an observation
ohara context [project]   Recent context from previous sessions
ohara stats               Memory statistics
ohara export [file]       Export all memories to JSON
ohara import <file>       Import memories from JSON
ohara projects list       Show all projects with counts
ohara projects consolidate  Interactive merge of similar project names
ohara projects prune      Remove projects with 0 observations
ohara version             Show version
```

---

## Next Steps

- [Agent Setup](AGENT-SETUP.md) — connect your agent to Ohara
- [Full Docs](../DOCS.md) — complete technical reference

