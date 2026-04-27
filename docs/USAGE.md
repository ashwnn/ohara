# Ohara — Complete Usage Guide

**Version:** v3 (spec-aligned)
**Last updated:** 2026-04-18

This document is a comprehensive reference for anyone working with Ohara — whether as a human collaborator, an AI agent, or an integrator connecting via MCP.

---

## Table of Contents

1. [What Ohara Is](#1-what-ohara-is)
2. [Core Concepts](#2-core-concepts)
3. [OpenCode Integration](#3-opencode-integration)
4. [MCP Tools Reference (31 tools)](#4-mcp-tools-reference-31-tools)
5. [Tool Profiles: What Loads When](#5-tool-profiles-what-loads-when)
6. [HTTP API Reference](#6-http-api-reference)
7. [CLI Commands](#7-cli-commands)
8. [Skills System](#8-skills-system)
9. [Contribution Workflow](#9-contribution-workflow)
10. [Session Lifecycle](#10-session-lifecycle)
11. [Memory Protocol](#11-memory-protocol)
12. [Conflict Detection and Resolution](#12-conflict-detection-and-resolution)
13. [Context Injection (Prime)](#13-context-injection-prime)
14. [Git Sync Mode](#14-git-sync-mode)
15. [Architecture](#15-architecture)
16. [Quick Reference Card](#16-quick-reference-card)

---

## 1. What Ohara Is

Ohara is a **persistent memory layer for AI coding agents**. It stores what agents learn — architectural decisions, bug fixes, patterns, procedures — and injects relevant context at the start of each session so agents don't repeat mistakes or re-discover what was already known.

**Key properties:**
- Single Go binary, zero runtime dependencies
- SQLite + FTS5 for storage and full-text search
- OpenCode native plugin + generic MCP stdio interface
- Works across multiple projects and multiple agents simultaneously
- No automatic event capture — all memory is agent-curated

---

## 2. Core Concepts

### 2.1 Memory Kinds

Every memory entry has a `kind` that determines its durability and injection priority:

| Kind | What it stores | Default classification |
|------|---------------|----------------------|
| `decision` | Architectural or design choices | foundational |
| `procedure` | Verified step-by-step workflows | foundational |
| `pattern` | Recurring solutions and conventions | tactical |
| `bugfix` | Root cause + resolution of bugs | tactical |
| `learned` | Non-obvious discoveries and gotchas | tactical |
| `discovery` | Raw session notes and memories | observational |

**Classification tiers** control shelf life:
- **foundational** — never auto-pruned, always included in context injection
- **tactical** — pruned when relevance decays below threshold
- **observational** — short TTL, excluded from injection by default (Episode tier)

### 2.2 Domain Scoping

Memories are scoped by subsystem (`auth`, `database`, `api`, `ci`, `infra`, `test`). This prevents cross-concern noise in retrieval as projects grow. **Always set domain when saving.**

### 2.3 Evidence Fields

For `decision` and `procedure` kinds, at least one evidence field should be provided: `commit`, `issue`, `file`, or `url`. This makes memories auditable and maintainable across code churn.

### 2.4 Actor-Aware Writes

Every write is tagged with its source (`written_by`):
- `user` — CLI writes
- `agent` — MCP `mem_save` calls
- `consolidation` — consolidated from multiple memories
- `import` — git sync imports
- `system` — setup, doctor commands

When retrieving memories tagged `[consolidation]` or `[import]`, treat as inference rather than verified fact.

### 2.5 Memory Lifecycle

```
Active → Expired (relevance decay or TTL) → Archived (forgotten)
Active → Superseded (replacement) → Archived
Active → Candidate (consolidation grouping) → Active (agent-promoted)
```

---

## 3. OpenCode Integration

### 3.1 Installation

```bash
# Source-build only
git clone https://github.com/ashwnn/ohara
cd ohara
go build -o ohara ./cmd/ohara

# Install OpenCode plugin
ohara setup opencode

# Or manually: cp plugin/opencode/ohara.ts ~/.config/opencode/plugins/
```

### 3.2 What the Plugin Does

The OpenCode plugin (`plugin/opencode/ohara.ts`) adds enhanced session management:

1. **Auto-starts** the ohara server if not running (no manual `ohara serve` needed)
2. **Creates sessions** on-demand via `ensureSession()` — resilient to restarts/reconnects
3. **Injects Memory Protocol** into the agent's system prompt via `chat.system.transform`
4. **Injects previous session context** into the compaction prompt
5. **Instructs the compressor** to persist compacted summary via `mem_session_summary`
6. **Strips `<private>` tags** before sending data
7. **Suppression of sub-agent sessions** — Task() subagents are not registered as top-level Ohara sessions (prevents session inflation, issue #116)
8. **Auto-import** — if `.ohara/manifest.json` exists, runs `ohara sync --import` on startup

Set `OHARA_DEBUG=1` when launching OpenCode to log failed plugin HTTP calls and
auto-start attempts. Normal mode stays quiet to avoid disrupting agent sessions.

The Memory Protocol is concatenated into the existing system prompt (not a separate system message), ensuring compatibility with models that only accept a single system block (Qwen, Mistral/Ministral via llama.cpp, etc.).

### 3.3 Three Layers of Memory Resilience

| Layer | Mechanism | Survives Compaction? |
|-------|-----------|---------------------|
| **System Prompt** | `MEMORY_INSTRUCTIONS` concatenated into existing system prompt | Always present |
| **Compaction Hook** | Auto-saves checkpoint + injects context + reminds compressor | Fires during compaction |
| **Agent Config** | "After compaction, call `mem_context`" in agent prompt | Always present |

### 3.4 Privacy

Wrap sensitive content in `<private>` tags — stripped at TWO levels:
1. Plugin layer — before data leaves the process
2. Store layer — `stripPrivateTags()` in Go before any DB write

```text
Set up API with <private>sk-abc123</private> key
→ Set up API with [REDACTED] key
```

### 3.5 Setup Commands (all agents)

```bash
ohara setup opencode          # Install OpenCode plugin
ohara setup claude-code        # Claude Code MCP config
ohara setup cursor            # Cursor MCP config
ohara setup windsurf           # Windsurf MCP config
ohara setup gemini-cli         # Gemini CLI MCP config
ohara setup vscode-copilot     # VS Code Copilot MCP config

ohara setup --check            # Verify integration is correct
ohara setup --remove <agent>  # Cleanly undo integration
```

---

## 4. MCP Tools Reference (31 tools)

Tools are organized by function. **Bold** tools are eager (always in context); others are deferred (loaded on demand).

### 4.1 Save & Update

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| **`mem_save`** | agent | eager | Save structured observation with full field support |
| `mem_update` | agent | deferred | Update existing memory by ID (partial update) |
| `mem_delete` | admin | deferred | Soft or hard delete by ID |
| `mem_forget` | agent | deferred | Archive with documented reason, preserves audit trail |
| `mem_suggest_topic_key` | agent | deferred | Stable topic key for upserts |

**`mem_save` full params:**
```
title* (string)           — Short, searchable title
content* (string)         — Structured **What**/**Why**/**Where**/**Learned** format
type (string)              — decision|architecture|bugfix|pattern|config|discovery|learning (default: manual)
session_id (string)        — Session to associate with
project (string)           — Project name
scope (string)             — project (default) | personal
topic_key (string)         — Stable key for upserts (e.g. architecture/auth-model)
domain (string)            — Subsystem: auth|database|api|ci|infra|test
classification (string)    — foundational|tactical|observational
written_by (string)       — user|agent|consolidation|import|system
expires_at (string)        — ISO timestamp for auto-expiry
trigger (string)           — When-X-happens condition for procedures
evidence (string)          — JSON: {commit, issue, file, url}
applies_to (string)        — JSON array of affected paths
related (string)           — JSON array of related memory IDs
force (boolean)            — Bypass governance checks
```

**When to call `mem_save`:**
- Bug fix completed
- Architecture or design decision made
- Non-obvious discovery
- Configuration change
- Pattern established
- User preference learned

### 4.2 Search & Retrieve

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| **`mem_search`** | agent | eager | FTS5 + optional hybrid search with filters |
| `mem_search_rerank` | agent | deferred | Optional slow-path LLM reranking on search results |
| **`mem_context`** | agent | eager | Recent session context via context pack |
| **`mem_prime`** | agent | eager | Structured prime context (markdown for system prompt) |
| **`mem_pack`** | agent | eager | Token-budgeted explicit context pack |
| `mem_timeline` | admin | deferred | Chronological context around a memory |
| `mem_graph_context` | agent | deferred | Entity-centric graph traversal |

**`mem_search` params:**
```
query* (string)            — Search query (natural language or keywords)
type (string)             — Filter by memory kind
project (string)          — Filter by project name
scope (string)            — project (default) | personal
domain (string)           — Filter by domain/subsystem
written_by (string)       — Filter by actor: user|agent|consolidation|import|system
limit (number)            — Max results (default: 10, max: 20)
min_confidence (number)   — Confidence threshold; returns low_confidence:true if exceeded
```

**Progressive disclosure pattern:**
1. `mem_search` → compact results with IDs (~100 tokens each)
2. `mem_timeline memory_id=N` → what happened before/after

### 4.3 Session Lifecycle

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| `mem_session_start` | agent | deferred | Register session start |
| `mem_session_end` | agent | deferred | Mark session completed |
| **`mem_session_summary`** | agent | eager | Save structured end-of-session summary |
| `mem_save_prompt` | agent | eager | Save user prompt |
| `mem_capture_passive` | agent | deferred | Extract learnings from text output |

**`mem_session_summary` content format:**
```
## Goal
[What we were working on]

## Instructions
[User preferences or constraints discovered]

## Discoveries
- [Technical findings, gotchas]

## Accomplished
- [Completed items with key details]

## Relevant Files
- path/to/file — [what it does]
```

### 4.4 Relations

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| `mem_link` | agent | deferred | Create typed relation between memories |
| `mem_unlink` | agent | deferred | Remove a relation |
| `mem_related` | agent | deferred | Traverse relations from a memory |

**Relation types:** `caused`, `resolves`, `supersedes`, `related_to`, `implements`, `contradicts`

### 4.5 Consolidation

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| `mem_consolidate_candidates` | agent | deferred | Review grouped episodic memories for consolidation |
| `mem_mark_consolidated` | agent | deferred | Archive source memories after consolidation |
| `mem_extract_entities` | agent | deferred | Extract and link entities from memory |

### 4.6 Feedback & Outcomes

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| `mem_mark_used` | agent | deferred | Record usage event (increments access_count) |
| `mem_append_outcome` | agent | deferred | Append success/failure/unknown outcome |
| `mem_feedback` | agent | deferred | Explicit utility feedback for RL weighting |

### 4.7 Utilities (Admin)

| Tool | Profile | Loading | Purpose |
|------|---------|---------|---------|
| `mem_stats` | admin | deferred | Memory system statistics |
| `mem_merge_projects` | admin | deferred | Merge project name variants |
| `mem_list_domains` | admin | deferred | List all distinct domains for a project |

---

## 5. Tool Profiles: What Loads When

The MCP server supports tool profiles to control which tools an agent sees:

```bash
ohara mcp                    → all 31 tools (default)
ohara mcp --tools=agent      → 26 tools agents actually use
ohara mcp --tools=admin       → 5 tools for TUI/CLI (delete, stats, timeline, merge, list_domains)
ohara mcp --tools=agent,admin → combine profiles
ohara mcp --tools=mem_save,mem_search → individual tool names
```

**Eager tools (always in context without ToolSearch):**
- mem_save, mem_search, mem_context, mem_session_summary, mem_save_prompt, mem_pack, mem_prime

**Agent profile (26 tools):** all tools except the 5 admin-only tools (mem_delete, mem_stats, mem_timeline, mem_merge_projects, mem_list_domains)

**Admin profile (5 tools):** mem_delete, mem_stats, mem_timeline, mem_merge_projects, mem_list_domains

---

## 6. HTTP API Reference

Base URL: `http://127.0.0.1:7331` (default port)

### Health

```
GET /health
→ { "status": "ok", "service": "ohara", "version": "0.1.0", "db_size_bytes": N, "memory_count": N }
```

### Sessions

```
POST /sessions
Body: { "id": "session-id", "project": "project-name", "directory": "/path" }
→ { "id": "...", "created": true }

PATCH /sessions/{id}
Body: { "summary": "session summary" }  ← ends session

POST /sessions/{id}/end
Body: { "summary": "session summary" }  ← legacy alias for PATCH

GET /sessions/{id}/context
→ Returns session context string

GET /sessions/recent
→ Returns recent session list

DELETE /sessions/{id}
```

### Prompts

```
POST /prompts
Body: { "session_id": "...", "content": "...", "project": "..." }

GET /prompts/recent
GET /prompts/search?query=...
DELETE /prompts/{id}
```

### Passive Capture

```
POST /capture/passive
Body: { "session_id": "...", "content": "...", "project": "...", "source": "..." }
→ { "extracted": N, "saved": N, "duplicates": N }
```

### Memory Items (v2 spec — current)

```
POST /memories
Body: { "project_id": "...", "kind": "...", "title": "...", "body": "...", ... }

GET /memories
GET /memories/search?query=...&domain=...&kind=...&limit=...
GET /memories/{id}
PATCH /memories/{id}
GET /memories/{id}/timeline
GET /memories/{id}/revisions
DELETE /memories/{id}
```

### Context & Pack

```
GET /context?project=...&scope=...
POST /pack
Body: { "project_id": "...", "budget_tokens": 2000, ... }
```

### Export / Import

```
GET /export
→ Returns full ExportData as JSON

POST /import
Body: ExportData JSON
→ Returns ImportResult
```

### Project Management

```
POST /projects/migrate
Body: { "old_project": "...", "new_project": "..." }
→ Returns migration result

GET /stats
→ Returns combined legacy + memory stats
```

### Sync Status

```
GET /sync/status
→ Returns autosync phase, last error, backoff time, last sync time
```

---

## 7. CLI Commands

### Core Commands

```bash
ohara serve           # Start HTTP server (port 7331)
ohara serve --socket /path/to/socket  # Unix socket mode
ohara version         # Show version
ohara stats            # Memory system statistics
ohara doctor           # Health analysis (no auto-fix)
ohara doctor --fix     # Health analysis with auto-fix
ohara validate        # Schema correctness check (non-zero exit on failure, for CI)
```

### Search & Retrieval

```bash
ohara search <query> [--domain <domain>] [--kinds <kinds>] [--format json|md]
ohara context [project] [--domain <domain>] [--budget <tokens>]
ohara prime [project] [--project <project>] [--domain <domain>] [--budget <tokens>]
```

### Session Management

```bash
ohara timeline <id>    # Show memory timeline
```

### Memory Operations

```bash
ohara save <title> --kind <kind> --content <content>  # CLI save (user mode)
ohara forget <id> --reason <reason>
```

### Projects

```bash
ohara projects list
ohara projects consolidate [--dry-run]
ohara projects consolidate --all
```

### Consolidation (top-level command)

```bash
ohara consolidate [--dry-run]    # Generate consolidation candidates
```

### Sync

```bash
ohara sync                # Sync with git-managed .ohara/ chunk directory
ohara sync --status       # Show sync status (local chunks, manifest chunks, pending import)
ohara sync --all          # Export all memories to .ohara/ directory
ohara sync --import       # Import new chunks from .ohara/ directory
```

### MCP Server

```bash
ohara mcp                 # Start MCP stdio server (all 31 tools)
ohara mcp --tools=agent  # Start with agent profile (26 tools)
ohara mcp --tools=admin  # Start with admin profile (5 tools)
```

### Setup

```bash
ohara setup opencode
ohara setup claude-code
ohara setup cursor
ohara setup windsurf
ohara setup gemini-cli
ohara setup vscode-copilot
ohara setup --check               # Verify integration
ohara setup --remove <agent>      # Remove integration
```

### Tools List

```bash
ohara tools               # List all available MCP tools
```

---

## 8. Skills System

Ohara uses a **skill-based instruction system** that triggers automatically based on the type of work being done. Skills are stored in `skills/` and symlinked to `.opencode/skills/` for OpenCode.

### 8.1 Available Skills

| Skill | Trigger | What it covers |
|-------|---------|---------------|
| `ohara-architecture-guardrails` | Changes affecting system boundaries, state flow, or cross-package responsibilities | Local-first rules, plugin thinness, decision assignment to packages |
| `ohara-branch-pr` | Creating a PR or preparing changes for review | Proposal-first workflow, branch naming, PR format, automated checks |
| `ohara-business-rules` | Changes to permissions, memory semantics, enrollment, or sync policy | Local-first default, org-wide controls, data visibility rules |
| `ohara-commit-hygiene` | Any commit creation, review, or branch cleanup | Conventional commits format, branch naming, pre-commit checklist |
| `ohara-cultural-norms` | Starting substantial work, reviewing changes, or defining conventions | Product coherence, explicit decisions, quality standards |
| `ohara-docs-alignment` | Any code or workflow change affecting user/contributor behavior | Docs must match code, update in same PR, validate examples |
| `ohara-issue-creation` | Creating a GitHub issue when Issues are enabled | Template usage, triage labels, problem statements |
| `ohara-memory-protocol` | Decisions, bugfixes, discoveries, or session closure | When to save, when to search, session close protocol |
| `ohara-plugin-thin` | Changes to plugin scripts/hooks for Claude, OpenCode, Gemini, Codex | Keep adapters thin, put logic in Go core, compatibility checklist |
| `ohara-pr-review-deep` | Reviewing any contribution before merge | Full diff review, test validation, API contract checks |
| `ohara-project-structure` | Creating files, packages, handlers, templates, styles, or tests | Placement rules, file creation rules, anti-patterns |
| `ohara-sdd-flow` | User requests SDD or multi-phase implementation planning | 5 canonical phases: explore → propose → apply → verify → archive |
| `ohara-server-api` | Any route, handler, payload, or status code modification | API contracts, E2E validation, migration safety |
| `ohara-testing-coverage` | Implementing behavior changes in any package | TDD loop, coverage rules, validation commands |
| `ohara-ui-elements` | Adding or changing dashboard UI components or connected browsing flows | UX rules, composition rules, connected flows |
| `ohara-visual-language` | Any dashboard styling, typography, spacing, or visual identity change | Visual rules, density rules, TUI-inspired palette |
| `ohara-backlog-triage` | Auditing open issues or PRs, triaging the backlog | Maintainer philosophy, disposition classification, triage report format |

### 8.2 How to Use Skills

1. Check the trigger column to find skills matching your current task
2. Load the skill by reading the SKILL.md file at the listed path
3. Follow ALL patterns and rules from the loaded skill
4. Multiple skills can apply simultaneously

---

## 9. Contribution Workflow

Ohara uses a **proposal-first workflow**. GitHub Issues may be disabled, so each
PR must describe the problem, scope, risk, and verification clearly in the PR
body. If Issues are enabled, linking one is useful but not required by CI.

### 9.1 Step-by-Step

```
1. Search existing issues/PRs if available
2. Keep the change focused to one problem
3. Open a PR with rationale, files touched, risk, and verification notes
4. Add exactly one type:* label
5. Wait for automated checks and review
```

### 9.2 PR Rules

- Keep PR scope focused — one logical change per PR
- Use [Conventional Commits](https://www.conventionalcommits.org/) format
  - Ensure all checks pass locally before pushing:
    - Unit:   `go test ./...`
    - Race:   `go test -race ./...`
    - E2E:    `go test -tags e2e ./internal/server/...`
    - Build:  `go build -trimpath ./cmd/ohara`
    - Vet:    `go vet ./...`
    - Vuln:   `govulncheck ./...`
- Update docs in the same PR when behavior changes
- Do not include `Co-Authored-By` trailers in commits

### 9.3 Automated PR Checks

Automated checks run on every PR and all must pass:

| Check | What it verifies |
|-------|-----------------|
| **Check PR Has type:* Label** | PR has exactly one `type:*` label |
| **Unit Tests** | `go test ./...` passes |
| **Race Tests** | `go test -race ./...` passes |
| **E2E Tests** | `go test -tags e2e ./internal/server/...` passes |
| **Build** | `go build -trimpath ./cmd/ohara` passes |
| **Vet** | `go vet ./...` passes |
| **Vulnerability Scan** | `govulncheck ./...` passes |

### 9.4 Label System

**Type Labels (required on every PR — pick exactly one):**

| Label | Use for |
|-------|---------|
| `type:bug` | Bug fixes |
| `type:feature` | New features |
| `type:docs` | Documentation-only changes |
| `type:refactor` | Code refactoring with no behavior change |
| `type:chore` | Maintenance, tooling, dependencies |
| `type:breaking-change` | Breaking changes |

**Status Labels (set by maintainers):**

| Label | Meaning |
|-------|---------|
| `status:needs-review` | Awaiting maintainer review |
| `status:approved` | Approved for implementation or merge |
| `status:in-progress` | Actively being worked on — auto-exempt from stale bot |
| `status:blocked` | Blocked by another issue or external dependency |
| `status:stale` | No activity for 30 days |
| `status:wontfix` | Intentionally not fixing |

### 9.5 Branch Naming (enforced by GitHub ruleset)

**Pattern:** `^(feat|fix|chore|docs|style|refactor|perf|test|build|ci|revert)\/[a-z0-9._-]+$`

**Rules:**
- Description MUST be lowercase
- Only `a-z`, `0-9`, `.`, `_`, `-` allowed in description

### 9.6 Commit Message Format (enforced by GitHub ruleset)

**Pattern:** `^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([a-z0-9\._-]+\))?!?: .+`

**Examples:**
```
feat(cli): add --json flag to session list command
fix(store): prevent duplicate observation insert on retry
docs(contributing): update workflow documentation
chore(deps): bump github.com/charmbracelet/bubbletea to v0.26
fix!: change session ID format
```

---

## 10. Session Lifecycle

### 10.1 Session Flow

```
Session starts → Agent works → Agent saves memories proactively
                                    ↓
Session ends → Agent writes session summary (Goal/Discoveries/Accomplished/Files)
                                    ↓
Next session starts → Previous session context is injected automatically
```

### 10.2 Session Start

At the start of a session:
1. OpenCode plugin calls `ensureSession()` automatically
2. Agent calls `mem_context` or `mem_prime` to get relevant context

### 10.3 During Work

**Save proactively after:**
- Completing a bug fix
- Making an architecture or design decision
- Discovering a non-obvious pattern or gotcha
- Changing configuration
- Learning a user preference

**Search proactively when:**
- Starting work that might overlap past sessions
- User says "remember" or references past work
- Beginning a new feature area

### 10.4 Session End

Before ending a session, the agent MUST call `mem_session_summary` with structured content.

**This is NOT optional.** If you skip this, the next session starts blind.

### 10.5 After Compaction

When an agent compacts (summarizes long conversations), it loses awareness of Ohara. Recovery steps:

1. Call `mem_session_summary` with the compacted summary
2. Call `mem_context` to recover previous context
3. Continue working

The OpenCode plugin handles this automatically via the compaction hook — it injects context from previous sessions and instructs the compressor to remind the new agent to call `mem_session_summary`.

---

## 11. Memory Protocol

The Memory Protocol is injected into the agent's system prompt by the OpenCode plugin. It is the authoritative set of rules for when to save, when to search, and how to close sessions.

### 11.1 When to Save

Call `mem_save` immediately after:
- decision
- bugfix
- pattern/discovery
- config/preference changes

Use structured content with `**What**`, `**Why**`, `**Where**`, `**Learned**` format.

Use stable `topic_key` for evolving topics (call `mem_suggest_topic_key` first).

### 11.2 When to Search

- On recall requests: `mem_context` first, then `mem_search`
- Before similar work: run proactive `mem_search`
- On first message: if user references the project, a feature, or a problem, call `mem_search` with their keywords before responding

### 11.3 Session Close Protocol

Before ending a session or saying "done" / "listo":
1. Call `mem_session_summary`
2. Include goal, discoveries, accomplished, next steps, relevant files

### 11.4 After Compaction

1. Save summary first
2. Recover context
3. Continue work

---

## 12. Conflict Detection and Resolution

### 12.1 Conflict Detection

Ohara detects contradictions at **save time** and **retrieval time**.

**Save-time**: When saving a memory, Ohara checks for conflicts with existing active memories in the same domain and kind.

**Retrieval-time**: After assembling search results, Ohara surfaces conflicts in the result set.

### 12.2 Resolution Actions

When `mem_resolve_conflict` is needed, choose the action by this logic:

| Action | When to use |
|--------|-------------|
| `add` | Both memories are correct and describe different things (false positive) |
| `merge` | Partial overlap — create a new canonical memory, both sources expire |
| `invalidate` | The older memory is actively wrong now — expire it, keep newer |
| `relate` | The memories are complementary views — add typed relation between them |
| `suppress` | The detected contradiction is known and acceptable — no structural change |

**Calling `mem_resolve_conflict`:**
```
obs_id_a* (number)      — First memory ID
obs_id_b* (number)      — Second memory ID
action* (string)        — add|merge|invalidate|relate|suppress
merged_content (string)  — Required for merge action
relation_type (string)  — Required for relate action (e.g. supersedes, contradicts, refines)
```

---

## 13. Context Injection (Prime)

### 13.1 What is Prime?

`mem_prime` builds a structured prime context with Knowledge vs Episode tier separation. It outputs compact markdown designed for direct injection into a system prompt — no JSON scaffolding to parse.

### 13.2 CLI Command

```bash
ohara prime [project] [--project <project>] [--domain <domain>] [--budget <tokens>]
```

### 13.3 MCP Tool

```bash
mem_prime(project_id*, domain, budget_tokens, kinds, files, format, show_actor)
```

**Params:**
- `project_id*` (string) — Project to build prime context for
- `domain` (string) — Filter by domain/namespace
- `budget_tokens` (number) — Token budget (default: 2000)
- `kinds` (string) — Filter by memory kinds (comma-separated)
- `files` (string) — Comma-separated file/path hints to bias matching
- `format` (string) — md (default) | xml | json
- `show_actor` (boolean) — Include [written_by] actor tags in output

### 13.4 Output Format (markdown default)

Only Knowledge-tier memories (foundational/tactical) are included by default. Episode-tier (observational) requires explicit filtering.

```markdown
## Ohara Context: <project> [<domain>]
Generated: <ISO timestamp> | Budget: <n> tokens

### Decisions
- **<title>** (<kind>) (<date>)
<content>

### Patterns
- **<title>**: <content>

### Known Failures
- **<title>**: <description>
  Resolution: <resolution>

### Procedures
- **<title>** (verified <date>)
  Trigger: <trigger condition>
  1. <step>
  2. <step>
```

### 13.5 Budget Behavior

`--budget` is a hard token cap. Truncation drops Episode-tier memories first, then oldest entries per section. Decisions and Patterns are preserved last.

---

## 14. Git Sync Mode

### 14.1 Overview

SQLite remains source of truth. The JSONL mirror (`.ohara/` directory) is a committable snapshot for git-portable project memory.

### 14.2 Commands

```bash
ohara sync                # Incremental sync (export new memories)
ohara sync --status       # Show sync status
ohara sync --all          # Export ALL memories to .ohara/
ohara sync --import       # Import new chunks from .ohara/
```

### 14.3 .ohara Directory Structure

```
.ohara/
  manifest.json           # Sync state and chunk tracking
  chunks/                 # Individual memory chunks
    memory_<id>.jsonl
    session_<id>.jsonl
    prompt_<id>.jsonl
```

### 14.4 Import Behavior

Import skips any record whose ID already exists locally. Content conflicts between imported and local records are surfaced as candidates for `mem_resolve_conflict` rather than auto-merging.

### 14.5 .gitignore Recommendation

```gitignore
# Remove this line to share agent memory with the repo
.ohara/
```

### 14.6 CI Integration

```yaml
- name: Restore Ohara memory
  run: ohara sync --import
```

---

## 15. Architecture

### 15.1 System Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                     AI Agents                               │
│   OpenCode (native plugin)  ·  Claude Code (MCP)          │
│   Gemini CLI (MCP)  ·  Cursor/Windsurf (MCP)               │
└────────────┬─────────────────┬─────────────────┬────────────┘
             │                 │                 │
             ▼                 ▼                 ▼
┌─────────────────────────────────────────────────────────────┐
│                   Ohara (single binary)                    │
│  ┌──────────┐  ┌──────────────┐  ┌────────────────────┐    │
│  │ CLI      │  │ MCP Server   │  │ HTTP API           │   │
│  │ (human)  │  │ (31 tools)   │  │ (port 7331)        │   │
│  └────┬─────┘  └──────┬───────┘  └────────┬───────────┘    │
│       │               │                   │                 │
│       └───────────────┴───────────────────┘                 │
│                           │                                 │
│                    ┌──────▼──────┐                        │
│                    │   Store     │                        │
│                    │  SQLite+    │                        │
│                    │  FTS5       │                        │
│                    └──────┬──────┘                        │
│                           │                                │
│       ┌───────────────────┼───────────────────┐           │
│       │                   │                   │              │
│   ┌───▼───┐         ┌────▼────┐        ┌─────▼─────┐     │
│   │ FTS5  │         │Relations│        │ Embeddings │     │
│   │ Index │         │  Graph  │        │ (Ollama)   │     │
│   └───────┘         └─────────┘        └───────────┘     │
└─────────────────────────────────────────────────────────────┘
```

### 15.2 Project Structure

```
ohara/
├── cmd/ohara/main.go           # CLI entrypoint + all commands
├── internal/
│   ├── store/                  # Core: SQLite + FTS5 + memory operations
│   │   ├── store.go            # Schema, migrations, sessions, stats
│   │   ├── memories.go         # Memory CRUD, conflict detection, access tracking
│   │   ├── pack.go             # Context pack and prime pack assembly
│   │   ├── hybrid.go           # FTS5 + embedding hybrid retrieval
│   │   └── graph_feedback.go    # Relation graph, entities, utility feedback
│   ├── server/server.go         # HTTP REST API (v2 spec aligned)
│   ├── mcp/mcp.go               # MCP stdio server (31 tools)
│   ├── config/config.go          # Configuration loading
│   ├── redact/redact.go          # Secret redaction pipeline
│   ├── maintain/maintain.go      # Archive, backup, integrity
│   ├── setup/setup.go            # Agent plugin installer (all 6 agents)
│   └── sync/                     # Git sync (JSONL chunk mirror)
├── plugin/                       # Agent plugins
│   └── opencode/ohara.ts        # OpenCode native plugin (TypeScript)
├── skills/                       # Agent instruction skills (17 skills)
└── go.mod / go.sum
```

### 15.3 Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Go over TypeScript | Single binary, cross-platform, no runtime |
| SQLite + FTS5 over vector DB | FTS5 covers most use cases; embeddings are opt-in |
| Agent-agnostic core | Go binary is the brain, thin plugins per-agent |
| Agent-driven compression | The agent already has an LLM, no need for another |
| Privacy at two layers | Strip in plugin AND store |
| Pure Go SQLite | No CGO, true cross-platform |
| No raw auto-capture | Curated summaries only |
| Zero LLM at retrieval time | Deterministic query latency, reranking is explicit opt-in |

---

## 16. Quick Reference Card

### First time in a session
```
mem_context  →  mem_search if needed
```

### After completing work
```
mem_save (structured: What/Why/Where/Learned)
```

### Before ending session
```
mem_session_summary (Goal/Discoveries/Accomplished/Files)
```

### When you find a conflict
```
mem_resolve_conflict with add|merge|invalidate|relate|suppress
```

### When something is actively wrong
```
mem_forget with reason and optional replacement_obs_id
```

### When pattern appears 3+ times
```
mem_consolidate_candidates → review → mem_save (new canonical) → mem_mark_consolidated
```

### When asked to remember
```
mem_search with relevant domain/kind filters
```

### After compaction
```
mem_session_summary (compacted content) → mem_context → continue
```

---

*Last updated: 2026-04-18*
