# Comparison: Ohara vs claude-mem

[← Back to README](../README.md)

[claude-mem](https://github.com/thedotmack/claude-mem) inspired Ohara, but they differ fundamentally:

| | **Ohara** | **claude-mem** |
|---|---|---|
| **Language** | Go (single binary, zero runtime deps) | TypeScript + Python |
| **Agent lock-in** | None. MCP stdio works with any agent | Claude Code only |
| **Search** | SQLite FTS5 (built-in, zero setup) | ChromaDB vector database (separate process) |
| **What gets stored** | Agent-curated summaries | Raw tool calls + AI compression |
| **Compression** | Agent does it inline (already has the LLM) | Separate Claude API calls |
| **Dependencies** | `go install` and done | Node.js 18+, Bun, uv, Python, ChromaDB |
| **Processes** | One binary | Worker service + ChromaDB |
| **Storage** | Single SQLite file | SQLite + ChromaDB (two systems) |
| **Web UI** | None (CLI + MCP only) | Web viewer on port 37777 |
| **Privacy** | `<private>` stripped at 2 layers | `<private>` stripped |
| **Auto-capture** | No. Agent decides what matters | Captures all tool calls then compresses |
| **License** | MIT | AGPL-3.0 |

## Philosophy

**claude-mem** captures everything, then compresses with AI — extra API calls, raw tool calls pollute search until compressed, requires worker process + ChromaDB + multiple runtimes, locked to Claude Code.

**Ohara** lets the agent decide what's worth remembering. The agent already has the LLM, context, and understands what just happened:

- `mem_save` after a bugfix: "Fixed N+1 query — added eager loading"
- `mem_session_summary` at session end: structured Goal/Discoveries/Accomplished/Files
- No noise, no compression step, no extra API calls, works with ANY MCP client
