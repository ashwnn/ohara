# Plugins

[← Back to README](../README.md)

## OpenCode Plugin

For [OpenCode](https://opencode.ai), a thin TypeScript plugin adds session management:

```bash
ohara setup opencode
```

Or manually copy `plugin/opencode/ohara.ts` to `~/.config/opencode/plugins/`.

The plugin auto-starts the HTTP server if not running — no manual `ohara serve` needed. Set `OHARA_DEBUG=1` for diagnostics.

### What It Does

- **Auto-starts** Ohara server if not running
- **Creates sessions** on-demand via `ensureSession()` (resilient to restarts)
- **Injects Memory Protocol** into the system prompt via `chat.system.transform`
- **Injects previous session context** into compaction prompts
- **Captures passive observations** to `POST /observe` (configurable breadth via `OHARA_PASSIVE_CAPTURE_LEVEL`)
- **Strips `<private>` tags** before sending data
- **Debug logs** when `OHARA_DEBUG=1`; normal mode stays quiet

Capture levels: `off`, `prompts` (default), `metadata`, `tools`, `full`.

Memory Protocol concatenated into the existing system message (not a separate one) — compatible with models using strict Jinja templates (Qwen, Mistral).

### Three Layers of Memory Resilience

| Layer | Survives Compaction? |
|-------|---------------------|
| System Prompt (MEMORY_INSTRUCTIONS) | Always present |
| Compaction Hook (auto checkpoint + context) | Fires during compaction |
| Agent Config ("after compaction, call mem_context") | Always present |

## Setup Commands (all agents)

```bash
ohara setup opencode       # OpenCode plugin
ohara setup claude-code    # Claude Code MCP config
ohara setup cursor         # Cursor MCP config
ohara setup windsurf       # Windsurf MCP config
ohara setup gemini-cli     # Gemini CLI MCP config
ohara setup vscode-copilot # VS Code Copilot MCP config
ohara setup --check        # Verify integration
ohara setup --remove <agent>
```

## Privacy

Wrap sensitive content in `<private>` tags — stripped at TWO levels:

```
Set up API with <private>sk-abc123</private> key
→ Set up API with [REDACTED] key
```

1. **Plugin layer** — stripped before data leaves the process
2. **Store layer** — stripped in Go before any DB write

Regex-based secret redaction is best effort. Treat Ohara as a local knowledge store, not a secret vault.
