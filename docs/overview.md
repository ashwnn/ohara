# Ohara × OpenCode Integration Guide

**Purpose**: How Ohara integrates with OpenCode, how Ohara works at the level relevant to that integration, why the design choices exist, and which OpenCode features are used and why.

**Audience**: AI agents working in this repository, or humans maintaining the integration.

**Grounding rule**: Major claims cite repo file paths. Anything not directly evidenced is labeled **Inference** with a TODO.

---

## 1. Overview

### 1.1 What is Ohara (in this repo)

Ohara is a persistent memory system for AI coding agents. It stores durable knowledge (decisions, procedures, patterns, bugfixes) and can also store short-lived "episodic" notes. It's designed to survive across sessions and compactions.

**Source**: `README.md`, `docs/PLUGINS.md`

### 1.2 What is OpenCode (as used here)

OpenCode is the agent runtime. It supports:
- plugins (TypeScript adapters) that hook lifecycle events
- multiple subagents with different models and roles
- MCP tools (Context7, Playwright, DeepWiki, PDF reader, Sequential Thinking, etc.)

**Source**: `AGENTS.md` (repo), global OpenCode directives (injected by OpenCode)

### 1.3 Integration goal

OpenCode sessions are ephemeral. Ohara is the long-term store. The OpenCode plugin (`plugin/opencode/ohara.ts`) bridges them by:
- starting / ensuring Ohara server is running
- registering sessions
- capturing prompts
- injecting memory protocol instructions into the system prompt
- handling compaction checkpoints

**Source**: `plugin/opencode/ohara.ts`

---

## 2. Integration Architecture

### 2.1 Component map

```
OpenCode (process)
  └── plugin/opencode/ohara.ts   ← TypeScript adapter (thin)
        │  HTTP calls
        ↓
ohara serve (Go binary, default port 7331)
  └── SQLite + FTS5 (local store)
```

Design intent: plugin stays thin; core memory logic lives in the Ohara binary.

**Source**: `plugin/opencode/ohara.ts`, `skills/plugin-thin/SKILL.md`

### 2.2 Plugin lifecycle hooks (what fires when)

The plugin registers OpenCode event handlers including:
- `session.created`: ensures an Ohara session exists
- `session.deleted`: cleans in-memory tracking
- `chat.message`: saves user prompt text
- `tool.execute.after`: tracks tool calls; can do optional passive capture
- `experimental.chat.system.transform`: injects memory protocol into the system prompt
- `experimental.session.compacting`: handles compaction survival (checkpoint + rehydrate)

**Source**: `plugin/opencode/ohara.ts`

### 2.3 Sub-agent session inflation guard

OpenCode subagent calls (`Task()`) can create nested sessions. Naively saving those as top-level Ohara sessions causes "session inflation" (many sessions for one conversation). The plugin avoids this by detecting subagents via heuristics (parentID or title suffix) and skipping `ensureSession()` for them.

**Source**: `plugin/opencode/ohara.ts` (subagent detection logic + issue note)

### 2.4 "Single system block" constraint

Some model chat templates only support one system message. Adding a second system message (for memory instructions) breaks them. The plugin appends memory instructions to the **last** existing system entry instead of pushing a new system message.

**Source**: `plugin/opencode/ohara.ts`

### 2.5 Auto-start and migration behavior

On load, plugin typically:
- checks Ohara health; spawns `ohara serve` if not running
- migrates project name if it changed
- imports git-synced memories if `.ohara/manifest.json` exists (via `ohara sync --import`)

**Source**: `plugin/opencode/ohara.ts`

---

## 3. How Ohara Works (integration-relevant subset)

### 3.1 Memory types + classification tiers

Ohara memories are typed (e.g. decision, bugfix, pattern, procedure) and have a **classification** that controls persistence and injection priority.

- **foundational**: never expires; core decisions/procedures
- **tactical**: medium-lived; patterns/bugfixes
- **observational**: short-lived; raw notes/episodes

**Source**: `README.md`, `AGENTS.md` (field conventions)

### 3.2 Context injection: "prime packs"

Ohara can build token-budgeted context packs (often markdown) intended for direct injection into agent prompts ("prime" behavior). It separates durable knowledge (high-signal) from episodic noise so compaction and limited context don't destroy the important parts.

**Source**: `README.md`, `docs/AGENT-SETUP.md`

### 3.3 Privacy: two-layer `<private>` redaction

Sensitive content wrapped in `<private>...</private>` is stripped:
1) in the plugin before sending text out
2) again in the Ohara store layer before DB write

Goal: defense in depth; avoid accidental secret retention.

**Source**: `plugin/opencode/ohara.ts`, `docs/PLUGINS.md`

---

## 4. The Memory Protocol (injected into system prompt)

The plugin injects `MEMORY_INSTRUCTIONS` into eligible agents' system prompts. This is the behavior contract agents are expected to follow.

**Source**: `plugin/opencode/ohara.ts` (the instruction block + injection hook)

### 4.1 When to save (mem_save)

Save immediately after:
- bug fix
- architecture/design decision
- non-obvious discovery/gotcha
- config/env change
- new recurring pattern/convention
- learned user preferences / constraints

Use structured content:

- **What**: what changed
- **Why**: reason / user ask
- **Where**: file paths touched
- **Learned**: gotchas / edge cases (optional)

**Source**: `plugin/opencode/ohara.ts`, `skills/memory-protocol/SKILL.md`

### 4.2 When to search (mem_context, mem_search)

- Proactive: at start of meaningful work that may overlap prior sessions
- Reactive: when user asks to remember/recall what happened

Typical flow:
1) `mem_context` (recent context pack)
2) `mem_search` (keywords) if needed

**Source**: `plugin/opencode/ohara.ts`, `skills/memory-protocol/SKILL.md`

### 4.3 Session closing (mem_session_summary)

Before ending, write a durable session summary with:
- Goal
- Instructions (preferences)
- Discoveries
- Accomplished
- Relevant Files

**Source**: `plugin/opencode/ohara.ts`, `skills/memory-protocol/SKILL.md`

### 4.4 Compaction survival

When compaction/reset happens, the protocol expects:
1) save a summary
2) reload context (`mem_context`)
3) continue

**Source**: `plugin/opencode/ohara.ts`

---

## 5. Routing & Agent Types (as used in this repo)

OpenCode uses specialized subagents. In this repo's conventions:
- `fast`: small, localized work; intentionally stateless
- `deep`: complex reasoning, multi-file changes, debugging, architecture
- `review`: read-only review
- `security`: security analysis / threat modeling / RE workflows
- `research`: investigations, docs deep-dives
- `planner`: planning-only
- `bug-hunter`: proactive bug scans

**Source**: `AGENTS.md` (routing table + decision rules)

### 5.1 Memory-aware agent set

Memory protocol injection is gated by `OHARA_MEMORY_AGENTS`. Default includes deep/security/research/planner-type roles, and excludes `fast` and `review` to avoid noisy writes and keep those roles lightweight.

**Source**: `plugin/opencode/ohara.ts`

---

## 6. OpenCode tools & MCP features (what to use, when, why)

### 6.1 Core MCP tools (expected usage)

| Tool | Use when | Why |
|---|---|---|
| Context7 | writing framework/library code | reduce hallucinated APIs; use current docs |
| Playwright | UI verification, browsing | screenshot/snapshot-based proof |
| DeepWiki | repo/library Q&A | grounded codebase understanding |
| PDF reader | PDFs | extract text/tables/images deterministically |
| Sequential Thinking | complex multi-step reasoning | avoid missed dependencies; structured reasoning |
| git tooling | history/diff/status | don't guess; verify |

**Source**: global OpenCode directives; `AGENTS.md` tool usage notes

### 6.2 Sequential Thinking trigger list (repo norm)

Use Sequential Thinking MCP for:
- multi-system interactions / state machines
- async ordering / concurrency
- architecture with dependency chains
- security threat modeling

**Source**: global OpenCode directives (routing guidance)

---

## 7. Skills system (Ohara-specific)

Ohara keeps repo-specific "skills" as rulesets (e.g. architecture guardrails, commit hygiene, PR review, plugin thinness). Agents are expected to load relevant skills before making changes.

**Source**: `AGENTS.md` (skill index), `skills/*/SKILL.md`

Key skill intents:
- `ohara-plugin-thin`: adapters must stay thin; core logic lives in Go
- `ohara-architecture-guardrails`: enforce boundaries (store vs cloud vs UI vs adapters)
- `ohara-commit-hygiene`: conventional commits + branch naming enforced by rulesets

**Source**: `skills/plugin-thin/SKILL.md`, `skills/architecture-guardrails/SKILL.md`, `skills/commit-hygiene/SKILL.md`

---

## 8. Operational Playbooks (when to use what)

### 8.1 OpenCode features → recommended playbooks

| Situation | Do this | Why |
|---|---|---|
| starting non-trivial work | load relevant skill(s), check `AGENTS.md`, then `mem_context` (memory-aware agents) | avoid repeating prior mistakes |
| implementing logic | `/tdd` workflow | prevents untested changes |
| multi-file feature | `/plan` then implement | prevents scope creep |
| before merge | `/review` | catch regressions and security mistakes |
| UI change | Playwright snapshot/screenshot | objective verification |
| compaction happened | `mem_session_summary` then `mem_context` | restore continuity |
| user says "remember" | `mem_context` then `mem_search` | fast retrieval first |

**Source**: `AGENTS.md`, `plugin/opencode/ohara.ts`, `skills/*`

### 8.2 Anti-patterns (repo norms)

- Don't run destructive commands without explicit approval.
- Don't run tests/install/commit/push without explicit approval.
- Don't `git add .`; stage specific files.
- Don't duplicate memory protocol text into other docs; it's injected at runtime.
- Don't add heavy logic into plugins; keep adapters thin.

**Source**: global OpenCode directives, `skills/plugin-thin/SKILL.md`

---

## 9. AI-optimized conventions (machine-friendly)

### 9.1 Memory field conventions (canonical)

**domain**: lowercase subsystem (`auth`, `database`, `k8s`, `api`, `ci`, `infra`, `test`)
**evidence**: required for `decision`/`procedure` (commit hash, issue ID, or file path)
**classification**: foundational/tactical/observational
**procedure trigger**: "When X happens …" and update via `mem_update` (don't duplicate)
**relations**: bugfix→`resolves`, replacement→`supersedes`, etc.
**forgetting**: prefer `mem_forget` (audit trail)

**Source**: `AGENTS.md`

### 9.2 "Where should this change live?" decision table

| Concern | Put it here |
|---|---|
| persistent store behavior | store layer (Go) |
| cloud replication/control plane | cloud store/server |
| UI/dashboard | dashboard package |
| background sync | autosync |
| OpenCode/Claude/Gemini adapters | plugin scripts (thin only) |

**Source**: `skills/architecture-guardrails/SKILL.md`, `skills/project-structure/SKILL.md`

---

## 10. Glossary

- **Ohara**: persistent memory system (local-first) for agents
- **OpenCode**: agent runtime with plugins, subagents, MCP tools
- **Plugin**: TypeScript adapter bridging OpenCode lifecycle → Ohara HTTP API
- **MEMORY_INSTRUCTIONS**: injected system prompt block describing save/search/close protocol
- **Prime pack**: token-budgeted memory context injected into prompts
- **Compaction**: context reset; new agent starts with a summary
- **Session inflation**: accidental creation of many sessions from subagent runs; plugin guards against it
- **Thin adapter**: rule: keep plugins as simple transport; avoid business logic

---

## 11. Reference file map (start here)

- `plugin/opencode/ohara.ts` — OpenCode integration: session tracking, injection, compaction hooks, privacy stripping
- `AGENTS.md` — repo agent rules, skills index, memory field conventions
- `README.md` — Ohara architecture and feature rationale
- `docs/PLUGINS.md` — plugin setup + privacy notes
- `docs/AGENT-SETUP.md` — agent integration notes/tool profiles
- `skills/memory-protocol/SKILL.md` — save/search/session-close discipline
- `skills/plugin-thin/SKILL.md` — adapter boundary rules
- `skills/architecture-guardrails/SKILL.md` — system boundaries/ownership
- `skills/commit-hygiene/SKILL.md` — commit/branch rules enforced by GitHub rulesets
