[← Back to README](../README.md)

# Agent Setup

Ohara supports **OpenCode** via native plugin and **any MCP-compatible agent** via bare MCP configuration.

## Quick Reference

| Agent | Setup |
|-------|-------|
| OpenCode | `ohara setup opencode` (plugin + MCP) |
| Any MCP agent | Manual MCP config pointing to `ohara mcp` |

---

## OpenCode (Recommended)

> **Prerequisite**: Build and install the `ohara` binary first (see [Installation](INSTALLATION.md)).

**Full setup with one command:**

```bash
ohara setup opencode
```

This copies the plugin to `~/.config/opencode/plugins/ohara.ts` and adds the MCP server to `opencode.json`.

The plugin also needs the HTTP server running for session tracking:

```bash
ohara serve &
```

> **Windows**: `ohara setup opencode` writes to `%APPDATA%\opencode\plugins\` and `%APPDATA%\opencode\opencode.json`.

**Manual MCP-only setup** (if you prefer not to use the plugin):

Add to your `opencode.json`:

```json
{
  "mcp": {
    "ohara": {
      "type": "local",
      "command": ["ohara", "mcp"],
      "enabled": true
    }
  }
}
```

---

## Other MCP Agents

For any agent that supports MCP (Claude Code, Cursor, VS Code, etc.), add this server configuration:

**Claude Code** — Add to `.claude/settings.json` or `~/.claude/settings.json`:
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

**Cursor** — Add to `.cursor/mcp.json`:
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

**VS Code** — Add to `.vscode/mcp.json`:
```json
{
  "servers": {
    "ohara": {
      "command": "ohara",
      "args": ["mcp"]
    }
  }
}
```

> **Note:** Only OpenCode has a native plugin with enhanced session management. All other agents use bare MCP without the additional plugin features (auto-session tracking, compaction recovery, etc.).

---

## Surviving Compaction (Recommended)

When your agent compacts (summarizes long conversations), it starts fresh and might forget about Ohara. Add this to your agent's system prompt:

**For OpenCode** (included automatically by the plugin, or add manually):
```
After any compaction or context reset, call mem_context to recover session state before continuing.
Save memories proactively with mem_save after significant work.
```

**For other MCP agents** (add to your agent's instructions):
```markdown
## Memory
You have access to Ohara persistent memory via MCP tools (mem_save, mem_search, mem_session_summary, etc.).
- Save proactively after significant work — don't wait to be asked.
- After any compaction or context reset, call `mem_context` to recover session state before continuing.
```

This is the **nuclear option** — system prompts survive everything, including compaction.

