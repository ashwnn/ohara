# Ohara — Agent Skills Index

When working on this project, load the relevant skill(s) BEFORE writing any code.

## How to Use

1. Check the trigger column to find skills that match your current task
2. Load the skill by reading the SKILL.md file at the listed path
3. Follow ALL patterns and rules from the loaded skill
4. Multiple skills can apply simultaneously

## Skills

| Skill | Trigger | Path |
|-------|---------|------|
| `ohara-architecture-guardrails` | Any change that affects system boundaries, ownership, state flow, or cross-package responsibilities. | [`skills/architecture-guardrails/SKILL.md`](skills/architecture-guardrails/SKILL.md) |
| `ohara-branch-pr` | When creating a pull request, opening a PR, or preparing changes for review. | [`skills/branch-pr/SKILL.md`](skills/branch-pr/SKILL.md) |
| `ohara-business-rules` | Any change that affects permissions, memory semantics, or data handling. | [`skills/business-rules/SKILL.md`](skills/business-rules/SKILL.md) |
| `ohara-commit-hygiene` | Any commit creation, review, or branch cleanup. | [`skills/commit-hygiene/SKILL.md`](skills/commit-hygiene/SKILL.md) |
| `ohara-cultural-norms` | Starting substantial work, reviewing changes, or defining team conventions. | [`skills/cultural-norms/SKILL.md`](skills/cultural-norms/SKILL.md) |
| `ohara-docs-alignment` | Any code or workflow change that affects user or contributor behavior. | [`skills/docs-alignment/SKILL.md`](skills/docs-alignment/SKILL.md) |
| `ohara-issue-creation` | When creating a GitHub issue, reporting a bug, or requesting a feature. | [`skills/issue-creation/SKILL.md`](skills/issue-creation/SKILL.md) |
| `ohara-memory-protocol` | Decisions, bugfixes, discoveries, preferences, or session closure. | [`skills/memory-protocol/SKILL.md`](skills/memory-protocol/SKILL.md) |
| `ohara-plugin-thin` | Changes in plugin scripts/hooks for Claude, OpenCode, Gemini, or Codex. | [`skills/plugin-thin/SKILL.md`](skills/plugin-thin/SKILL.md) |
| `ohara-pr-review-deep` | Reviewing any external or internal contribution before merge. | [`skills/pr-review-deep/SKILL.md`](skills/pr-review-deep/SKILL.md) |
| `ohara-project-structure` | Creating files, packages, handlers, templates, styles, or tests in this repo. | [`skills/project-structure/SKILL.md`](skills/project-structure/SKILL.md) |
| `ohara-sdd-flow` | When user requests SDD or multi-phase implementation planning. | [`skills/sdd-flow/SKILL.md`](skills/sdd-flow/SKILL.md) |
| `ohara-server-api` | Any route, handler, payload, or status code modification. | [`skills/server-api/SKILL.md`](skills/server-api/SKILL.md) |
| `ohara-testing-coverage` | When implementing behavior changes in any package. | [`skills/testing-coverage/SKILL.md`](skills/testing-coverage/SKILL.md) |
| `ohara-ui-elements` | Adding or changing dashboard UI components or connected browsing flows. | [`skills/ui-elements/SKILL.md`](skills/ui-elements/SKILL.md) |
| `ohara-visual-language` | Any dashboard styling, typography, spacing, or visual identity change. | [`skills/visual-language/SKILL.md`](skills/visual-language/SKILL.md) |
| `ohara-backlog-triage` | Auditing open issues or PRs, triaging the backlog, or reviewing contributor submissions as a maintainer. | [`skills/backlog-triage/SKILL.md`](skills/backlog-triage/SKILL.md) |

---

**Note:** Skill names use the `ohara-*` prefix.

---

## Memory — Contributor Field Guide

Runtime protocol (when to save, search, session close) is injected by the OpenCode plugin (`plugin/opencode/ohara.ts`).
Do not duplicate those instructions here. This section covers **field conventions** only.

| Field | Convention |
|-------|-----------|
| **domain** | Lowercase subsystem: `auth`, `database`, `k8s`, `api`, `ci`, `infra`, `test`. Keep consistent across sessions. |
| **evidence** | Required for `decision` and `procedure` types. At least one of: commit hash, issue ID, or file path. |
| **classification** | `foundational` (never expires) for core decisions/procedures. `tactical` (default) for patterns/bugfixes. `observational` for raw notes. |
| **procedure** | Set `trigger` to a "When X happens" phrase. Update via `mem_update` on original `obs_id`, don't duplicate. |
| **relations** | `mem_link` patterns: bugfix→`resolves`, procedure→`implements`, replacement→`supersedes`, causal→`caused`. |
| **conflict resolution** | `add` (both valid), `merge` (canonical), `invalidate` (wrong→replace), `relate` (complementary), `suppress` (acceptable coexistence). |
| **forgetting** | Prefer `mem_forget` over `mem_delete` — preserves audit trail. |
| **actor tags** | `[consolidation]`/`[import]` memories are inferences. Confirm against source before acting. `written_by` is set automatically. |
