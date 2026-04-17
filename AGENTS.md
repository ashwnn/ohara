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

## Memory Usage Guidelines

The following guidelines apply when using Ohara's memory tools. These are based on the features now implemented.

### On domain

Always set domain when saving a memory. Use the subsystem name in lowercase:
"auth", "database", "k8s", "api", "ci", "infra", "test". Keep domain strings
consistent across sessions. When searching, pass domain to narrow results if
you know the relevant subsystem.

### On evidence

For decision and procedure memories, always set evidence with at least one of:
commit hash, issue ID, or file path. This makes the memory auditable and allows
file-targeted prime packs. For foundational memories, evidence is required before
the record is considered active.

### On classification

Set classification when you have a strong signal: "foundational" for decisions and
procedures that should never auto-expire, "tactical" for patterns and bugfixes with
medium durability, "observational" for raw session notes. The default is "tactical".
Foundational memories are never auto-pruned.

### On procedure type

Use kind "procedure" for step-by-step workflows you have personally verified work
in this project. Set trigger_condition to a "When X happens" phrase so future searches
can match on intent. Set evidence.verified_at. Update via mem_update on the original
obs_id rather than saving a duplicate when the procedure changes.

### On relations

After saving a memory that directly relates to an existing one, call mem_link to
record the relationship. Common patterns:
  - A bugfix that resolves a known pattern:       relation "resolves"
  - A procedure that implements a decision:       relation "implements"
  - A new decision that replaces an old one:      relation "supersedes"
  - A decision that caused a later bug:           relation "caused"

### On actor-aware retrieval

When a memory returned by mem_search or mem_prime is tagged [consolidation] or
[import], treat it as an inference rather than a directly verified fact. Confirm
against current environment state or a source memory before acting on it.
When calling mem_save, you do not need to set written_by - it is set automatically.

### On conflict resolution

When mem_resolve_conflict is needed, choose the action by this logic:
  - Both memories are correct and describe different things → "add"
  - Partial overlap, should be one canonical memory → "merge"
  - Old memory is actively wrong, new one replaces it → "invalidate"
  - Memories are complementary, not contradictory → "relate"
  - Known acceptable coexistence of contradictory facts → "suppress"

### On forgetting

When replacing an old decision with a new one, call mem_forget on the old obs_id
with a clear reason. Prefer mem_forget over mem_delete: mem_forget preserves the
audit trail. mem_delete physically removes the row and should be reserved for
sensitive data removal.
