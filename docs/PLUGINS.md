[← Back to README](../README.md)

# Plugins

- [OpenCode Plugin](#opencode-plugin)
- [Privacy](#privacy)

---

## OpenCode Plugin

For [OpenCode](https://opencode.ai) users, a thin TypeScript plugin adds enhanced session management on top of the MCP tools:

```bash
# Install via ohara (source-build only)
ohara setup opencode

# Or manually: cp plugin/opencode/ohara.ts ~/.config/opencode/plugins/
```

The plugin auto-starts the HTTP server if it's not already running — no manual `ohara serve` needed.

By default, failed plugin calls are quiet so OpenCode remains usable even when
Ohara is not installed or not running. For diagnostics, start OpenCode with
`OHARA_DEBUG=1` to log failed health checks, HTTP writes, auto-start attempts,
and sync imports to stderr.

> **Local model compatibility:** The plugin works with all models, including local ones served via llama.cpp, Ollama, or similar. The Memory Protocol is concatenated into the existing system prompt (not added as a separate system message), so models with strict Jinja templates (Qwen, Mistral/Ministral) work correctly.

### What the Plugin Does

The plugin:
- **Auto-starts** the ohara server if not running
- **Creates sessions** on-demand via `ensureSession()` (resilient to restarts/reconnects)
- **Injects the Memory Protocol** into the agent's system prompt via `chat.system.transform` — strict rules for when to save, when to search, and a mandatory session close protocol. The protocol is concatenated into the existing system message (not pushed as a separate one), ensuring compatibility with models that only accept a single system block (Qwen, Mistral/Ministral via llama.cpp, etc.)
- **Injects previous session context** into the compaction prompt
- **Instructs the compressor** to tell the new agent to persist the compacted summary via `mem_session_summary`
- **Strips `<private>` tags** before sending data
- **Debug logs when requested** via `OHARA_DEBUG=1`; normal mode stays quiet

**No raw tool call recording** — the agent handles all memory via `mem_save` and `mem_session_summary`.

### Memory Protocol (injected via system prompt)

The plugin injects a strict protocol into every agent message:

- **WHEN TO SAVE**: Mandatory after bugfixes, decisions, discoveries, config changes, patterns, preferences
- **WHEN TO SEARCH**: Reactive (user says "remember"/"recordar") + proactive (starting work that might overlap past sessions)
- **SESSION CLOSE**: Mandatory `mem_session_summary` before ending — "This is NOT optional. If you skip this, the next session starts blind."
- **AFTER COMPACTION**: Immediately call `mem_context` to recover state

### Three Layers of Memory Resilience

The OpenCode plugin uses a defense-in-depth strategy to ensure memories survive compaction:

| Layer | Mechanism | Survives Compaction? |
|-------|-----------|---------------------|
| **System Prompt** | `MEMORY_INSTRUCTIONS` concatenated into existing system prompt via `chat.system.transform` | Always present |
| **Compaction Hook** | Auto-saves checkpoint + injects context + reminds compressor | Fires during compaction |
| **Agent Config** | "After compaction, call `mem_context`" in agent prompt | Always present |

---

## Privacy

Wrap sensitive content in `<private>` tags — it gets stripped at TWO levels:

```
Set up API with <private>sk-abc123</private> key
→ Set up API with [REDACTED] key
```

1. **Plugin layer** — stripped before data leaves the process
2. **Store layer** — `stripPrivateTags()` in Go before any DB write

Regex-based secret redaction is best effort. Treat Ohara as a local knowledge
store, not a password manager or secret vault.
