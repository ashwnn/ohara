[← Back to README](../README.md)

# Agent Integration

Ohara supports **OpenCode** via native plugin and **any MCP-compatible agent** via MCP stdio.

OpenCode gets the full experience — auto session tracking, compaction recovery, proactive save prompts. All other agents (Claude Code, Cursor, VS Code, Gemini CLI, etc.) connect via MCP and get the same memory tools without the plugin extras.

## Tool Profiles

The MCP server supports tool profiles to control which tools an agent sees:

- **agent** — 26 tools agents use in practice (save, search, context, prime, link, consolidate, etc.)
- **admin** — 5 tools for manual curation (delete, stats, timeline, merge, list_domains)
- **all** — everything (default)

This keeps the agent's tool list focused. Agents that only need memory operations don't see admin tools.

## Compaction Recovery

When an agent compacts (summarizes long conversations), it loses awareness of Ohara. The system prompt is the only thing that survives compaction — so agents that call `mem_context` immediately after a reset can recover their session state from what was previously saved.
