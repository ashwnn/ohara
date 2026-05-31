# Memory Envelope v1

Ohara now supports a client-neutral Memory Envelope v1 for memory writes.

## Scope of this change

- Applies to Ohara local store, CLI, MCP, and HTTP write paths.
- Does not modify Mission Control.
- Does not add Claude Code integration.
- Preserves existing OpenCode behavior and legacy write payloads.

## Envelope model

Envelope data is persisted in `memory_items.evidence_json` as JSON with:

- `schema_version`
- `scope`
- `project_id`
- `session_id`, optional `run_id`
- `source.client`, `source.agent`, `source.executor`, `source.written_by`
- `memory.kind`, `memory.classification`, optional `memory.topic_key`, `memory.title`, `memory.content`
- `evidence.cwd`, `evidence.git_remote`, `evidence.git_root`, `evidence.git_branch`, `evidence.git_commit`, `evidence.files`, `evidence.worktree`, `evidence.external_refs`
- `trust.level`, `trust.reason`
- `lifecycle.status`, `lifecycle.expires_at`, `lifecycle.supersedes`

## Defaults and normalization

- `schema_version=1`
- Scope supports `task | project | global` and keeps `personal` for backward compatibility.
- Kind aliases:
  - `learned` -> `discovery`
  - `preference` -> `user_preference`
  - `resume_state` -> `config`
- Classification defaults:
  - `decision`/`procedure` -> `foundational`
  - `discovery` -> `observational`
  - others -> `tactical`
- Trust defaults:
  - `high` when local trusted client + git commit evidence
  - `medium` when local cwd/git metadata exists
  - `low` otherwise (including import/consolidation)

## Project identity

Ohara now has a central project identity helper in `internal/project` and a CLI command:

- `ohara project-id`

Project ID rules:

1. Git repo + origin remote:
   `slug(repo-name) + "-" + short-hash(normalized-remote-url)`
2. Git repo, no remote:
   `slug(git-root-basename) + "-" + short-hash(git-root-path)`
3. No git:
   `slug(cwd-basename) + "-" + short-hash(cwd-path)`

Remote normalization removes trailing `.git` and trailing `/`.

## Idempotency

- Added `memory_items.idempotency_key` (migration 28).
- Write dedupe occurs only when:
  - caller passes `idempotency_key`, or
  - `OHARA_WRITE_MODE=idempotent` is set (key auto-derived from project/scope/title/content/session/git commit).

This keeps existing behavior unchanged unless idempotency is explicitly requested.

## Client hint environment variables

Optional hints now supported by envelope defaults:

- `OHARA_CLIENT`
- `OHARA_AGENT`
- `OHARA_EXECUTOR`
- `OHARA_SESSION_ID`
- `OHARA_PROJECT`
- `OHARA_PROJECT_ID`
- `OHARA_CWD`
- `OHARA_WORKTREE`
- `OHARA_WRITE_MODE`

## Safety note

Redaction and validation reduce accidental secret persistence risk, but regex redaction is not a hard security boundary.
