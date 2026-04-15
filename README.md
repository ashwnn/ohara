<p align="center">
  <img src="assets/ohara.png" alt="Ohara" width="128" /><br>
  <strong>Ohara</strong><br>
  <em>Typed persistent memory with conflict detection for AI coding agents</em><br>
  <small>OpenCode plugin + MCP only. Source-build only. No TUI or marketplace.</small>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> &bull;
  <a href="docs/AGENT-SETUP.md">Agent Setup</a> &bull;
  <a href="docs/ARCHITECTURE.md">Architecture</a> &bull;
  <a href="DOCS.md">Full Docs</a>
</p>

---

> **Ohara** is a renamed personal fork of [**Engram**](https://github.com/Gentleman-Programming/engram), originally created by [**Gentleman-Programming**](https://github.com/Gentleman-Programming). The entire foundation of this project — the core memory system, MCP architecture, and concept of persistent agent memory — was built by the Engram authors. This fork adds ecosystem-specific features (typed memory, conflict detection, automatic lifecycle management) for personal use; the original work and all credit belong to Gentleman-Programming.

Your AI coding agent forgets everything when the session ends. Ohara gives it a brain.

A **Go binary** with SQLite + FTS5 full-text search, exposed via CLI, HTTP API, and **MCP server**. Supports **OpenCode** via native plugin, plus any agent that speaks MCP.

**What makes Ohara different from standard Engram:**

| Feature | Standard Engram | Ohara |
|---------|----------------|-------|
| **Typed memory** | Free-form strings | Structured memory types (`decision`, `bugfix`, `pattern`, `learned`) |
| **Conflict detection** | None — silent overwrites | Save-time contradiction detection with revision history |
| **Revisions** | Single version | Full revision history with `mem_timeline` |
| **Expiry/Archive** | Manual cleanup | Automatic lifecycle: `active` → `expired` → `archived` |

These features improve memory quality by catching contradictions before they pollute your context, and let you audit how your project's understanding evolved over time. For agents that reason across long-running projects, this prevents drift and maintains coherent context.

**This fork intentionally retains MCP** as the primary interface. Only OpenCode has a native plugin; all other agents use MCP directly. TUI, marketplace, and publishing-oriented surfaces are not part of this fork's direction.

```
Agent (OpenCode / Any MCP-compatible agent)
    ↓ MCP stdio
Ohara (single Go binary)
    ↓
SQLite + FTS5 (~/.local/share/ohara/ohara.db)
```

## Quick Start

### Install (Source Build Only)

```bash
git clone https://github.com/ashwnn/ohara.git
cd ohara
go build -o ohara ./cmd/ohara
```

See [docs/INSTALLATION.md](docs/INSTALLATION.md) for full build instructions.

### Setup Your Agent

**OpenCode** (recommended — full plugin with session management):
```bash
ohara setup opencode
```

**Any MCP-compatible agent** (bare MCP, no plugin):
```json
{
  "mcpServers": {
    "ohara": {
      "command": "ohara",
      "args": ["mcp"]
    }
  }
}
```

Full setup details and Memory Protocol → [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md)

That's it. **One binary, one SQLite file.**

## How It Works

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save → title, type, What/Why/Where/Learned
3. Ohara persists to SQLite with FTS5 indexing
4. Next session: agent searches memory, gets relevant context
```

Full details on session lifecycle → [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## MCP Tools (15)

| Category | Tools |
|----------|-------|
| **Save & Update** | `mem_save`, `mem_update`, `mem_delete`, `mem_suggest_topic_key` |
| **Search & Retrieve** | `mem_search`, `mem_context`, `mem_timeline`, `mem_get_observation` |
| **Session Lifecycle** | `mem_session_start`, `mem_session_end`, `mem_session_summary` |
| **Utilities** | `mem_save_prompt`, `mem_stats`, `mem_capture_passive`, `mem_merge_projects` |

Full tool reference → [DOCS.md#mcp-tools-15-tools](DOCS.md#mcp-tools-15-tools)

## CLI Reference

| Command | Description |
|---------|-------------|
| `ohara setup [agent]` | Install agent integration |
| `ohara serve [port]` | Start HTTP API (default: 7331) |
| `ohara mcp` | Start MCP server (stdio) |
| `ohara search <query>` | Search memories |
| `ohara save <title> <msg>` | Save a memory |
| `ohara timeline <obs_id>` | Chronological context |
| `ohara context [project]` | Recent session context |
| `ohara stats` | Memory statistics |
| `ohara export [file]` | Export to JSON |
| `ohara import <file>` | Import from JSON |
| `ohara projects list\|consolidate\|prune` | Manage project names |
| `ohara version` | Show version |

Full CLI → [docs/ARCHITECTURE.md#cli-reference](docs/ARCHITECTURE.md#cli-reference)

## Documentation

| Doc | Description |
|-----|-------------|
| [Installation](docs/INSTALLATION.md) | Build from source |
| [Agent Setup](docs/AGENT-SETUP.md) | Per-agent configuration + Memory Protocol |
| [Architecture](docs/ARCHITECTURE.md) | How it works + MCP tools + project structure |
| [Full Docs](DOCS.md) | Complete technical reference |

## Fork Status

This is a **personal fork** maintained for individual use. It:

- Retains the MCP server and HTTP API (the core memory interface)
- Uses source-build installation only
- Does **not** include TUI, or publishing-oriented surfaces
- Tracks upstream Engram selectively

For the original project with full features (TUI, sync, etc.), see [**Gentleman-Programming/engram**](https://github.com/Gentleman-Programming/engram).

## License

MIT (same as upstream Engram)

</invoke>