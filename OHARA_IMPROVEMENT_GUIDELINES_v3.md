# Ohara - Developer Improvement Guidelines

**Date:** 2026-04-15 (updated)
**Scope:** Go/SQLite/MCP architecture - no new runtime dependencies for P0 or P1.
**Deployment context:** Central memory layer for all agentic work across multiple
projects and agents (OpenCode, Claude Code, and others). This is not a single-project
tool. Design decisions must account for multi-project noise, multi-agent write sources,
session accumulation at scale, and cross-project retrieval quality degradation over time.

> Based on a comparative analysis against [Mulch](https://github.com/jayminwest/mulch),
> [Graphiti/Zep](https://github.com/getzep/graphiti) (arXiv:2501.13956),
> [Mem0](https://github.com/mem0ai/mem0) (arXiv:2504.19413),
> [Letta/MemGPT](https://github.com/letta-ai/letta),
> [Supermemory](https://github.com/supermemoryai/supermemory),
> [mcp-memory-libsql-go](https://github.com/ZanzyTHEbar/mcp-memory-libsql-go),
> [code-review-graph](https://github.com/tirth8205/code-review-graph),
> [MemPalace](https://github.com/milla-jovovich/mempalace),
> and a review of current AI agent memory research: CoALA taxonomy (arXiv:2309.02427),
> Mem0 (arXiv:2504.19413), MemRL (arXiv:2601.03192), AgeMem (arXiv:2601.01885),
> Mem^p (arXiv:2508.06433), and the December 2025 survey (arXiv:2512.13564).

---

## Table of Contents

1. [Baseline Analysis](#1-baseline-analysis)
2. [Memory Model Background](#2-memory-model-background)
3. [Objectives](#3-objectives)
4. [P0 - Quick Wins (1-3 days each)](#4-p0---quick-wins-1-3-days-each)
   - [4.1 Domain / Namespace Field](#41-domain--namespace-field)
   - [4.2 Evidence, Provenance, and Relation Fields](#42-evidence-provenance-and-relation-fields)
   - [4.3 `ohara prime` - Context Injection Command](#43-ohara-prime---context-injection-command)
   - [4.4 `ohara validate` and `ohara doctor`](#44-ohara-validate-and-ohara-doctor)
   - [4.5 Classification Tiers](#45-classification-tiers)
5. [P1 - Quality Improvements (1-2 weeks)](#5-p1---quality-improvements-1-2-weeks)
   - [5.1 Temporal Decay and Usage Scoring](#51-temporal-decay-and-usage-scoring)
   - [5.2 Outcome Tracking](#52-outcome-tracking)
   - [5.3 Retrieval-Time Conflict Surfacing](#53-retrieval-time-conflict-surfacing)
   - [5.4 Conflict Resolution Workflows](#54-conflict-resolution-workflows)
   - [5.5 Temporal Fields and Time-Scoped Retrieval](#55-temporal-fields-and-time-scoped-retrieval)
   - [5.6 Abstention on Low-Confidence Retrieval](#56-abstention-on-low-confidence-retrieval)
   - [5.7 Bi-temporal Model](#57-bi-temporal-model)
   - [5.8 Actor-Aware Writes](#58-actor-aware-writes)
   - [5.9 Store-Time TTL and Explicit Forgetting](#59-store-time-ttl-and-explicit-forgetting)
   - [5.10 Four-Choice Update Resolver](#510-four-choice-update-resolver)
6. [P2 - Portability and Collaboration (2-6 weeks)](#6-p2---portability-and-collaboration-2-6-weeks)
   - [6.1 Git Sync Mode (JSONL Mirror)](#61-git-sync-mode-jsonl-mirror)
   - [6.2 `procedure` Memory Type](#62-procedure-memory-type)
   - [6.3 Relation Graph and `mem_related`](#63-relation-graph-and-mem_related)
   - [6.4 Provider Setup Recipes](#64-provider-setup-recipes)
   - [6.5 Sleep-Time Consolidation Worker](#65-sleep-time-consolidation-worker)
7. [P3 - Architectural Additions (6-12 weeks)](#7-p3---architectural-additions-6-12-weeks)
   - [7.1 Hierarchical Consolidation](#71-hierarchical-consolidation)
   - [7.2 Hybrid Retrieval: FTS5 + Embeddings](#72-hybrid-retrieval-fts5--embeddings)
   - [7.3 Temporal Knowledge Graph (Optional Index)](#73-temporal-knowledge-graph-optional-index)
   - [7.4 RL-Informed Memory Scoring](#74-rl-informed-memory-scoring)
   - [7.5 Evaluation Harness](#75-evaluation-harness)
8. [Security Requirements](#8-security-requirements)
9. [Schema Migration Reference](#9-schema-migration-reference)
10. [MCP Tool Changelist](#10-mcp-tool-changelist)
11. [Agent Instruction Updates (AGENTS.md)](#11-agent-instruction-updates-agentsmd)
12. [Definition of Done Checklists](#12-definition-of-done-checklists)
13. [Suggested Work Order](#13-suggested-work-order)

---

## 1. Baseline Analysis

### 1.1 What Ohara does well - do not regress

- Single Go binary, zero runtime dependencies.
- SQLite + FTS5 for reliable, low-latency keyword search.
- Typed memory schema (`decision`, `bugfix`, `pattern`, `learned`).
- Save-time conflict detection with full revision history.
- Automatic lifecycle: `active` -> `expired` -> `archived`.
- Session lifecycle management via MCP (`mem_session_start/end/summary`).
- Native OpenCode plugin + generic MCP stdio interface.

### 1.2 What Mulch does that Ohara should learn from

| Mulch pattern | What it solves |
|---------------|---------------|
| Domain-scoped queries (`mulch query database`) | Noise in retrieval as projects grow |
| Evidence fields (commit, issue, file, url) | Memories are auditable and maintainable |
| Classification tiers (foundational/tactical/observational) | Drives shelf life and pruning rules |
| `mulch prime` - agent-optimized injection pack | Agents waste tokens on JSON scaffolding |
| `mulch validate` + `mulch doctor` | Silent DB degradation over time |
| `.mulch/` JSONL in repo, git-tracked | Project memory lost when switching machines |
| Advisory locks + `merge=union` JSONL strategy | Concurrent agent write safety |

### 1.3 Identified gaps (Mulch + research sources)

| Gap | Source |
|-----|--------|
| No domain/namespace scoping | Mulch |
| No evidence/provenance/relation fields | Mulch |
| No classification tiers | Mulch |
| No optimised context injection format | Mulch |
| No schema/DB validation or health tooling | Mulch |
| No git-portable project memory | Mulch |
| Conflicts only detected at save time | Research (Mem0 2025) |
| Lifecycle is binary state, not scored relevance | Research (temporal decay) |
| No outcome tracking or usage signals | Research (Evo-Memory) |
| No episodic-to-semantic consolidation | Research (CoALA, AgeMem) |
| No procedural/skill memory type | Research (Voyager, Mem^p) |
| No inter-memory relation graph | Research (Mem0 graph, MAGMA) |
| No temporal scoped retrieval (`asof:`, `since:`) | Research (LongMemEval) |
| No abstention on low-confidence results | Research (MemoryAgentBench) |
| No security hardening (trust levels, audit log) | Research + operational risk |
| FTS5 only - no semantic/vector search | Research (Mem0 hybrid retrieval) |

### 1.3b Identified gaps (additional - from broader ecosystem analysis)

The following gaps emerged from a comparative review of Graphiti/Zep (arXiv:2501.13956),
Mem0 (arXiv:2504.19413 + Group-Chat v2), Letta/MemGPT, Supermemory, LangMem, and OpenMemory.
These complement the Mulch-derived gaps above.

| Gap | Source |
|-----|--------|
| No bi-temporal model: event time and ingestion time are not distinguished | Graphiti/Zep |
| Conflict resolution is detect-and-flag only; no structured 4-choice resolver | Mem0 |
| No actor-aware write tagging (user vs agent vs subagent vs import) - critical when multiple agents write to the same DB | Mem0 Group-Chat v2 |
| No per-entry store-time TTL or explicit forgetting tool | Supermemory |
| Hybrid retrieval design does not constrain to zero LLM calls at query time | Graphiti/Zep |
| Consolidation is agent-blocking; no async sleep-time consolidation mode - compounds across many active projects | Letta |
| Eval harness does not include forgetting quality as a dimension | AgeMem, MemoryAgentBench |
| `ohara prime` does not tag injected memories by actor or type origin | OpenMemory MCP |

---

### 1.4 Broader Ecosystem Comparison

| System | Architecture | Key pattern to learn from | Maps to Ohara |
|--------|-------------|--------------------------|---------------|
| **Graphiti/Zep** | Python + Neo4j/FalkorDB, temporal KG, Apache 2.0 | Bi-temporal model (event time vs ingestion time); hybrid semantic+BM25+graph retrieval with zero LLM calls at query time; P95 retrieval at 300ms | Bi-temporal fields (5.7); zero-LLM retrieval constraint (7.2) |
| **Mem0 / OpenMemory** | Python + vector/graph/KV store, managed + local MCP | Two-phase pipeline (extract then update); 4-choice Update Resolver (add/merge/invalidate/skip); actor-aware writes; project-scoped MCP | 4-choice resolver (5.10); actor-aware writes (5.8); validates P0 domain approach |
| **Letta / MemGPT** | Python, full agent runtime, Apache 2.0 | OS-tier memory hierarchy (core/archival/recall); sleep-time async consolidation; agent self-edits own procedural instructions | Sleep-time worker (6.5); two-tier injection (episode vs knowledge) |
| **Supermemory** | Closed + MCP | Store-time TTL per entry; explicit `forget` API; auto-retire contradicted and temporally expired facts | Store-time TTL + `mem_forget` (5.9) |
| **LangMem** | Python, LangGraph extension, MIT | Episodic/semantic/procedural memory types as first-class; procedural memory modelled as updateable system instructions | Confirms P2 `procedure` kind; agents update procedure memories mid-session as normal writes |
| **MemPalace** | Python + ChromaDB + SQLite TKG | AAAK token-efficient compression for context injection; hierarchical palace structure for retrieval scoping; temporal KG with validity windows in SQLite | `ohara prime` output format; P3.3 TKG schema design |
| **mcp-memory-libsql-go** | Go + libSQL (SQLite fork), MIT | Same stack as Ohara; per-project DB isolation; vector search in Go with zero Python dependency | Direct reference implementation for P3.2 embedding sidecar |
| **code-review-graph** | Python + Tree-sitter | Vertical codebase memory: AST-derived knowledge graph for blast-radius analysis | Complementary to Ohara (run alongside); entity model reference for P3.3 |

---

### High-level framing

Ohara is a strong *store and retrieval* layer. Mulch is a strong *governed knowledge workflow* layer. The opportunity is to add workflow, governance, portability, and evaluation rigor to Ohara without losing "one binary, one DB."

---

## 2. Memory Model Background

The field has converged on the **CoALA taxonomy** (Princeton, 2023, arXiv:2309.02427),
which defines four functional memory types. Every major agent memory framework now builds
on this model.

| Type | What it stores | Coding agent example |
|------|----------------|----------------------|
| **Working** | Active context window | What the agent is reasoning about right now |
| **Episodic** | Records of specific past events tied to time | "In session 2025-11-10, fixed JWT race by adding mutex" |
| **Semantic** | Abstracted, de-contextualised project knowledge | "This project always uses WAL mode for SQLite" |
| **Procedural** | Verified, reusable step-by-step workflows | "To add a new Kubernetes Goat scenario: do X, Y, Z" |

**Where Ohara stands today**: `decision`, `bugfix`, `pattern`, and `learned` collapse
episodic, semantic, and procedural content into a single flat table. This works at small
scale but degrades as projects grow - consolidation, conflict detection, and context
injection all become less precise without type-level signal.

A two-tier memory convention (from recent 2026 consolidation research) aligns with this:

- **Episode tier**: raw incident/interaction notes, short TTL, not injected by default
- **Knowledge tier**: distilled decisions, procedures, patterns, long TTL, injected by `prime`

The improvements below progressively move toward proper separation without requiring a
full rewrite.

---

## 3. Objectives

**Objective A - Raise memory quality** (signal, correctness, durability)
- Reduce duplicate/contradictory memories beyond title similarity.
- Improve retrieval ranking to return the right 3-7 items reliably across many projects.
- Make memory entries actionable: evidence, outcomes, and links.

**Objective B - Raise operational usability for multiple projects and agents**
- Git-native sharing mode so project memory can travel with a repo.
- Multi-agent write source tracking so Claude Code, OpenCode, and automated agents
  can be distinguished at retrieval time.
- Safer multi-agent concurrency for write-heavy, multi-project workflows.
- Better ergonomics: `prime` injection packs, file-based filters, health tooling.
- Background consolidation so session accumulation across many projects does not
  degrade retrieval quality without manual intervention.

**Objective C - Align with current research without overengineering**
- Add self-evolving and hierarchical memory mechanisms incrementally.
- Introduce graph/temporal modules only where they materially improve retrieval.
- Never reintroduce automatic event capture. Keep agent-curated writes as the default.

---

## 4. P0 - Quick Wins (1-3 days each)

---

### 4.1 Domain / Namespace Field

#### Problem

All memories for a project are queried together with no way to narrow by subsystem.
As a project grows, `mem_search` returns increasingly noisy results across unrelated
concerns.

This is the single highest-leverage change: every subsequent feature (consolidation,
conflict detection, `prime`) becomes significantly more useful when scoped to a domain
rather than operating across a flat global pool.

#### Schema change

```sql
ALTER TABLE observations ADD COLUMN domain TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_obs_project_domain ON observations(project_key, domain);
```

#### MCP tool changes

| Tool | Change |
|------|--------|
| `mem_save` | Add optional `domain` string param |
| `mem_search` | Add optional `domain` filter param |
| `mem_context` | Add optional `domain` filter param |
| `mem_stats` | Add per-domain count breakdown |
| `mem_save_prompt` | Include domain guidance in returned prompt |
| New: `mem_list_domains` | Return all distinct domains for a project |

#### CLI changes

```
ohara search <query> [--domain <domain>]
ohara context [project] [--domain <domain>]
ohara stats [--domain <domain>]
```

---

### 4.2 Evidence, Provenance, and Relation Fields

#### Problem

Memories have no machine-readable link to the artefacts that justify them (commits,
issues, files). Over time it becomes impossible to audit whether a `decision` or
`pattern` is still valid, which file it applies to, or whether it has been superseded.

Mulch treats evidence as a first-class schema concern. It enables file-targeted `prime`
packs, reduces irrelevant retrieval, and makes memories maintainable across code churn.

#### Schema changes

Add JSON columns to `observations` (stored as text, parsed at query time):

```sql
ALTER TABLE observations ADD COLUMN evidence_json   TEXT DEFAULT '{}';
-- { "commit": "abc123", "issue": "GH-42", "file": "internal/auth/token.go", "url": "..." }

ALTER TABLE observations ADD COLUMN applies_to_json TEXT DEFAULT '{}';
-- { "files": ["internal/auth/"], "paths": ["cmd/ohara/"], "commands": ["make test-kg"] }

ALTER TABLE observations ADD COLUMN related_json    TEXT DEFAULT '{}';
-- { "relates_to": ["obs_id_1"], "supersedes": ["obs_id_2"], "derived_from": ["obs_id_3"] }
```

If a dedicated relation table is preferred over JSON columns, see section 6.3 for the
full graph implementation. For P0, JSON columns are sufficient and require no joins.

#### MCP tool changes

- `mem_save`: accept `evidence`, `applies_to`, `related` as optional structured params.
- `mem_search`: support `file:` and `path:` filter prefixes to scope retrieval to
  memories whose `applies_to_json` matches.

#### Note on high-impact kinds

For `decision` and `procedure` kinds, require at least one evidence field before saving
(enforced at write time with a clear error message, not silent). This is a soft
governance gate, not a hard block - `--force` bypasses it.

---

### 4.3 `ohara prime` - Context Injection Command

#### Problem

`mem_context` returns raw structured JSON. There is no purpose-built format designed for
injection into an agent system prompt. Agents waste context tokens on JSON scaffolding
rather than substance.

Mulch's `mulch prime` emits a compact, markdown-structured block designed for direct
pipe into a system prompt.

#### Proposed CLI command

```
ohara prime [project] [--domain <domain>] [--budget <tokens>] [--files <path,...>]
            [--kinds <decision,pattern,...>] [--tags <tag,...>] [--format md|xml|json]
```

- `--budget`: hard token cap. Truncates by dropping Episode-tier memories first, then
  oldest entries per section. Decisions and Patterns are preserved last.
- `--files`: bias toward memories whose `applies_to_json` matches the given paths.
- `--format`: default `md`. XML for agents that prefer structured tags.

#### Output format (markdown default)

Only Knowledge-tier memories are included by default. Episode-tier (session notes)
require `--include-episodes`.

```markdown
## Ohara Context: <project> [<domain>]
Generated: <ISO timestamp> | Memories: <count> | Budget: <n> tokens

### Decisions
- **<title>** (<date>): <content>
  Evidence: <commit or issue if set>

### Patterns
- **<title>**: <content>

### Known Failures
- **<title>**: <description>
  Resolution: <resolution>

### Procedures
- **<title>** (verified <date>):
  Trigger: <trigger condition>
  1. <step>
  2. <step>

### Active Conflicts
- WARNING: '<title A>' may contradict '<title B>' - review before acting.
```

#### New MCP tool

`mem_prime` - equivalent to the CLI command, returning the formatted string for agent
injection.

Params: `project_key` (required), `domain`, `budget` (default `2000`), `files`,
`kinds`, `tags`, `format`.

---

### 4.4 `ohara validate` and `ohara doctor`

These are two separate commands with distinct roles.

**`ohara validate`** - fast schema correctness check, fails hard on structural errors.
Run this in CI.

```
ohara validate [--project <project>]
```

Checks: required fields present, field lengths within limits, tag format valid,
`related_json` references point to existing `obs_id` values, `classification` is a
valid value, `kind` is in the allowed set.

Output: pass/fail per check with the offending `obs_id`. Non-zero exit code on any
failure.

---

**`ohara doctor`** - health analysis with optional auto-fix. Run this periodically.

```
ohara doctor [--project <project>] [--fix]
```

Checks run by doctor:

| Check | Description | Auto-fixable |
|-------|-------------|:---:|
| Duplicate content | Two `active` memories with >90% normalised content overlap | Suggests merge |
| Dead file references | `applies_to_json` paths that no longer exist on disk | No - flag |
| Stale knowledge | `procedure` or `config` kind not updated in X days | No - flag |
| Orphaned revisions | `revisions` rows with no matching `obs_id` | Yes - delete |
| Stuck lifecycle | `active` memories with `updated_at` older than threshold and `access_count = 0` | Yes - expire |
| Unresolved conflicts | Conflicts table entries unactioned beyond N days | No - flag |
| Project key fragmentation | Same logical project under slightly different key strings | No - flag, suggest `ohara projects consolidate` |

With `--fix`: auto-apply safe remediations (orphan cleanup, stuck expiry, tag
normalisation). Flag-only items print suggestions and require manual action.

Output format:

```
ohara doctor --project ohara

  [PASS] No orphaned revisions.
  [WARN] 3 memories not accessed in 180+ days. Run with --fix to expire.
  [FAIL] 2 duplicate active memories in domain "auth" (obs_id: abc123, def456).
  [FAIL] 1 procedure "Deploy to staging" not updated in 90 days.

2 failures, 1 warning. Run --fix to auto-remediate WARN items.
FAIL items require manual review.
```

---

### 4.5 Classification Tiers

#### Problem

All memories are treated as equally durable. A raw session note should not have the
same shelf life as an architectural decision. Mulch solves this with classification
tiers that drive shelf life, pruning rules, and injection priority.

#### Schema change

```sql
ALTER TABLE observations ADD COLUMN classification TEXT NOT NULL DEFAULT 'tactical'
  CHECK(classification IN ('foundational', 'tactical', 'observational'));
```

#### Default mapping per kind

| Kind | Default classification |
|------|----------------------|
| `decision` | foundational |
| `procedure` | foundational |
| `pattern` | tactical |
| `bugfix` | tactical |
| `learned` | tactical |
| `discovery` (session note) | observational |

Override is allowed per-memory. Classification drives:

- **foundational**: never auto-pruned, always included in `prime` unless budget forces
  exclusion, require evidence fields.
- **tactical**: pruned if `relevance_score < threshold` (see 5.1).
- **observational**: Episode-tier, excluded from `prime` by default, short TTL.

---

## 5. P1 - Quality Improvements (1-2 weeks)

---

### 5.1 Temporal Decay and Usage Scoring

#### Problem

Two `active` memories are treated as equally relevant regardless of one being accessed
yesterday and the other eight months ago. The binary lifecycle is too blunt.

#### Schema changes

```sql
ALTER TABLE observations ADD COLUMN access_count  INTEGER  NOT NULL DEFAULT 0;
ALTER TABLE observations ADD COLUMN last_accessed DATETIME;
```

Increment `access_count` and update `last_accessed` on every retrieval by any MCP tool.

Add a `memory_usage` table to support `mem_mark_used` (distinct from implicit access):

```sql
CREATE TABLE IF NOT EXISTS memory_usage (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id TEXT    NOT NULL REFERENCES observations(obs_id),
  event     TEXT    NOT NULL CHECK(event IN ('retrieved', 'used')),
  session_id TEXT,
  ts        DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_usage_memory ON memory_usage(memory_id);
```

New MCP tool: `mem_mark_used` - agent calls this after a task to mark which retrieved
memories were actually applied in the response. This is the simpler precursor to RL
scoring (section 7.4).

#### Relevance score formula

Computed at query time, not stored. Applied to `mem_search` and `mem_context` ordering:

```
relevance_score = fts5_bm25_rank
                * (1.0 / (1.0 + days_since_last_access * decay_rate))
                * log(1.0 + access_count)
                * outcome_boost          (see 5.2, default 1.0 until outcomes are tracked)
```

Configurable in `ohara.config`:

```yaml
retrieval:
  decay_rate: 0.03        # higher = faster staleness penalty
  score_floor: 0.05       # memories below this threshold auto-transition to expired
```

- Sort results by `relevance_score DESC`. Include `relevance_score` in returned JSON.
- `foundational` memories are exempt from `score_floor` expiry.
- Replace fixed-duration expiry trigger with `relevance_score < score_floor` check on
  each retrieval and during a scheduled background sweep.

---

### 5.2 Outcome Tracking

#### Problem

Ohara has no feedback loop on whether a memory was *correct*. A `bugfix` whose
resolution turned out to be wrong is treated identically to one that has been
verified ten times.

#### Schema addition

```sql
CREATE TABLE IF NOT EXISTS memory_outcomes (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id TEXT    NOT NULL REFERENCES observations(obs_id),
  status    TEXT    NOT NULL CHECK(status IN ('success', 'failure', 'unknown')),
  notes     TEXT,
  actor_id  TEXT,
  ts        DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_outcomes_memory ON memory_outcomes(memory_id);
```

#### New MCP tool

`mem_append_outcome` - params: `obs_id`, `status` (success|failure|unknown), `notes`
(optional).

#### Ranking integration

Compute an `outcome_boost` from the outcomes table per memory, and include it in the
relevance score formula (5.1):

```
success_count = COUNT WHERE status = 'success'
failure_count = COUNT WHERE status = 'failure'
outcome_boost = 1.0 + 0.2 * success_count - 0.3 * failure_count
outcome_boost = MAX(outcome_boost, 0.1)  -- floor, never fully suppress
```

Memories with repeated `failure` outcomes should be surfaced to `ohara doctor` as
candidates for review or deletion.

---

### 5.3 Retrieval-Time Conflict Surfacing

#### Problem

Ohara detects contradictions at save time only. Two failure modes are not covered:

1. A conflict that slipped through save-time detection.
2. An `expired` memory that re-enters relevance and now contradicts an `active` one.

#### Implementation

After assembling the top-N results in `mem_search` or `mem_context`:

1. Query the existing `conflicts` table for any `obs_id` pairs in the result set.
   These are `severity: "high"` - previously verified contradictions.

2. For pairs not already flagged, run a fast heuristic over pairs sharing the same
   `project_key + domain + kind`: flag pairs where one entry contains
   `"always"/"use"/"enable"` and the other contains `"never"/"avoid"/"disable"` for the
   same subject noun. Mark these `severity: "low"` (heuristic, not verified).

#### Response format

Append only when conflicts exist:

```json
{
  "memories": [...],
  "conflicts": [
    {
      "obs_id_a": "abc123",
      "obs_id_b": "def456",
      "summary": "'Use WAL mode for all connections' may contradict 'Disable WAL for read-only replicas'",
      "severity": "high"
    }
  ]
}
```

Omit the `conflicts` field entirely on clean responses. This is read-only logic added
to the retrieval path - no schema migration required beyond indexing the conflicts table.

---

### 5.4 Conflict Resolution Workflows

#### Problem

Current conflict detection flags contradictions but offers no structured resolution
path. The agent or developer has to manually sort it out.

#### New duplicate/conflict policy actions

When `mem_save` detects a conflict, or when `ohara doctor` surfaces a duplicate, the
system should support three resolution actions:

| Action | Effect |
|--------|--------|
| `merge` | Create a new canonical memory that supersedes both sources; sources transition to `expired` with `superseded_at` set |
| `link` | Auto-add `relates_to` between the two records; keep both active |
| `suppress` | Record that this pair should not trigger future conflict warnings |

New MCP tool: `mem_resolve_conflict` - params: `obs_id_a`, `obs_id_b`, `action`
(merge|link|suppress), `merged_content` (required if action is `merge`).

---

### 5.5 Temporal Fields and Time-Scoped Retrieval

#### Problem

There is no way to query "what did we know about X in February" or to express that a
memory was valid only within a certain time window. This matters for projects where
conventions change, APIs are deprecated, or environment configs are replaced.

Research on LongMemEval highlights temporal reasoning as a core competency gap.

#### Schema changes

```sql
ALTER TABLE observations ADD COLUMN valid_from     DATETIME;
ALTER TABLE observations ADD COLUMN valid_to       DATETIME;
ALTER TABLE observations ADD COLUMN superseded_at  DATETIME;
```

- `valid_from`: when this knowledge became true (defaults to `created_at`).
- `valid_to`: when this knowledge stopped being true (set on expiry or supersession).
- `superseded_at`: when a newer memory replaced this one.

#### Retrieval operator additions

Add to `mem_search` and CLI `ohara search`:

- `asof:<ISO_timestamp>`: return only memories that were `active` at that point in time
  (i.e. `valid_from <= ts` and (`valid_to` is null or `valid_to > ts`)).
- `since:<ISO_timestamp>`: return only memories created or updated after that timestamp.
- `session:<session_id>`: return memories saved in a specific session.

Store `session_id` at save time (accept from agent if provided, else generate):

```sql
ALTER TABLE observations ADD COLUMN session_id TEXT DEFAULT '';
```

---

### 5.6 Abstention on Low-Confidence Retrieval

#### Problem

Returning a low-relevance memory is often worse than returning nothing. Agents that
receive uncertain context may act on it, producing incorrect outputs.

Research from MemoryAgentBench frames "abstain vs hallucinate" as a core evaluation
metric.

#### Implementation

Add an optional `min_confidence` param to `mem_search`:

- If the highest-scoring result's `relevance_score` is below `min_confidence`, return
  an empty result set with a `low_confidence: true` flag rather than returning poor
  matches.
- Default: no minimum (preserves current behaviour). Agents can set their preferred
  threshold in `mem_save_prompt` guidance.

```json
{
  "memories": [],
  "low_confidence": true,
  "message": "No memories met the minimum confidence threshold of 0.3 for query 'auth token refresh'."
}
```

---

### 5.7 Bi-temporal Model

#### Problem

Ohara stores `created_at` (when the row was inserted) and `valid_from`/`valid_to` (when
the knowledge is valid), but treats them interchangeably. Graphiti/Zep (arXiv:2501.13956)
identified the separation of event time from ingestion time as the core primitive that
enables reliable temporal reasoning.

Two distinct queries are currently conflated:

- "What did we know as of March 1?" (knowledge-time) - uses `valid_from`/`valid_to`
- "When did the event this memory describes actually happen?" (event-time) - needs a
  separate field

Across many projects with long-running session histories, retroactive memory entries
are common: logging an architectural decision made last sprint, importing from a git
sync that happened three days ago, consolidating session notes from last week. Without
`ingested_at` as a separate immutable field, `asof:` queries become unreliable and the
audit log loses its ability to answer "what did the agent know at time T."

#### Schema change

```sql
-- Migration 015: Bi-temporal fields (P1 - 5.7)
ALTER TABLE observations ADD COLUMN ingested_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
-- Immutable after write. Distinct from created_at (which can be updated on mem_update).
-- valid_from: when the described event/decision took effect (already added in 5.5)
-- valid_to:   when it stopped being true (already added in 5.5)
```

`ingested_at` is set at write time and is never modified, even by `mem_update`.
`valid_from` defaults to `ingested_at` but may be set earlier (retroactive entry)
or later (future-dated convention).

#### Retrieval operator additions

Add to `mem_search` and `ohara search`:

- `asof:<ISO_timestamp>`: existing operator now uses `valid_from`/`valid_to` (event-time)
- `ingested_asof:<ISO_timestamp>`: new operator - return only memories that existed in
  the DB at that point (i.e. `ingested_at <= ts`). Useful for debugging and auditing
  what the agent knew at a given moment.

#### `ohara doctor` check

Flag memories where `valid_from` is more than 30 days earlier than `ingested_at` -
these are retroactive entries that may have been misdated.

---

### 5.8 Actor-Aware Writes

#### Problem

In a workflow using multiple agents (OpenCode, Claude Code, automated scripts,
consolidation jobs, git imports), the source of a write is unknown at retrieval time.
Mem0's Group-Chat v2 (2025) identified actor-aware memory as critical for multi-agent
correctness: a planning agent's inference treated as user ground-truth by a downstream
agent produces systematic error propagation.

For Ohara used as a central memory layer across many projects, the same issue arises
continuously. A consolidation job that abstracts ten session notes into one `learned`
memory is one inference removed from the original events. An import from a git sync
may be stale. The agent calling `mem_search` has no way to weight these differently
without a source field.

This is not a hypothetical multi-agent concern - it is an everyday reality when
OpenCode, Claude Code, and `ohara sync import` all write to the same DB.

#### Schema change

```sql
-- Migration 016: Actor-aware writes (P1 - 5.8)
ALTER TABLE observations ADD COLUMN written_by TEXT NOT NULL DEFAULT 'agent'
  CHECK(written_by IN ('user', 'agent', 'consolidation', 'import', 'system'));
```

Default assignment rules:

| Write path | `written_by` value |
|-----------|-------------------|
| `ohara save` CLI | `user` |
| `mem_save` MCP tool | `agent` |
| `ohara sync import` | `import` |
| `mem_mark_consolidated` (source memories) | `consolidation` |
| `ohara setup`, `ohara doctor --fix` | `system` |

#### Retrieval integration

- `mem_search` accepts optional `written_by` filter.
- `mem_prime` output optionally appends `[agent]`, `[consolidation]` tags beside entries
  so the receiving agent can weight accordingly (enable via `--show-actor` flag).
- `mem_stats` includes per-actor breakdown.

#### Agent instruction

Add to AGENTS.md after 5.8 ships: "When a retrieved memory is tagged `[consolidation]`
or `[import]`, treat it as an inference rather than a verified fact. Prefer to confirm
against a source memory or current environment state before acting on it."

---

### 5.9 Store-Time TTL and Explicit Forgetting

#### Problem

Ohara's existing lifecycle (`active → expired → archived`) is driven by `decay_rate`
config and `relevance_score` thresholds. There is no way to express at write time that
a memory is only valid until a specific date, and there is no agent-callable tool to
deliberately retire a memory with a documented reason.

Supermemory's approach: temporary facts ("the deployment window opens tomorrow") expire
automatically without cleanup code. Mem0 Platform uses store-time `expiration_date` per
entry. Both avoid the accumulation of noise that degrades retrieval quality over time.

Research ("Forgetful but Faithful", 2025) formalizes six forgetting policies including
Priority Decay and Hybrid, showing that intelligent forgetting improves both retrieval
accuracy and reduces context noise.

#### Schema change

```sql
-- Migration 017: Store-time TTL (P1 - 5.9)
ALTER TABLE observations ADD COLUMN expires_at DATETIME;
-- NULL = no store-time TTL; uses decay_rate lifecycle instead.
-- Non-null = hard expiry date; transition to expired at this timestamp
-- regardless of relevance_score.
```

#### Background sweep addition

Extend the existing decay sweep to also check `expires_at <= CURRENT_TIMESTAMP` and
transition those memories to `expired`. Foundational classification memories ignore
`expires_at` (log a warning if set on a foundational entry but do not expire).

#### Suggested TTL defaults by classification (document in AGENTS.md)

| Classification | Suggested `expires_at` | Notes |
|---------------|----------------------|-------|
| `foundational` | Never - ignore if set | Architectural decisions |
| `tactical` | 90 days from `ingested_at` | Most patterns and bugfixes |
| `observational` | 7 days from `ingested_at` | Session notes, raw discoveries |
| (agent override) | Any date | Agent sets explicit date on save |

Agents set `expires_at` via `mem_save` when they know the temporal scope. Example:
saving a note about a temporary config change that expires after the next release.

#### New MCP tool: `mem_forget`

Explicit, audited retirement. Different from `mem_delete` (which physically removes the
row). `mem_forget` archives with provenance.

Params: `obs_id` (required), `reason` (required, free text), `replacement_obs_id`
(optional, link to the superseding memory).

Effect:
- Sets `status = 'archived'`
- Sets `valid_to = CURRENT_TIMESTAMP`
- Writes to `audit_log` with reason
- If `replacement_obs_id` is provided, creates a `supersedes` relation via
  `memory_relations`

This preserves the full audit trail while removing the memory from all active retrieval.
Ohara will never return archived memories in `mem_search` or `mem_prime` by default.

#### Schema migration

```sql
-- (expires_at already added above - no additional migration needed)
-- mem_forget is a new MCP tool only; no schema addition required beyond the audit_log
-- already specified in section 8.
```

---

### 5.10 Four-Choice Update Resolver

#### Problem

Section 5.4 defines three conflict resolution actions (merge, link, suppress). Mem0
(arXiv:2504.19413) uses a four-choice Update Resolver that better models all real-world
cases: `add` (false positive - both memories coexist without conflict), `merge` (combine
into canonical), `invalidate` (older memory is superseded and should expire), `skip`
(incoming memory is a true duplicate, discard it).

The `link` action in section 5.4 is renamed `relate` (the existing `mem_link` tool
already uses relation vocabulary; keeping the word consistent reduces confusion). The
`suppress` action is retained as a distinct choice from `invalidate` - suppressing a
conflict warning without changing memory state is useful when two memories describe the
same decision from different perspectives.

#### Updated resolution action table

| Action | Effect | When to use |
|--------|--------|-------------|
| `add` | No action on existing memories; mark the conflict detection as a false positive | The two memories describe different things and both are correct |
| `merge` | Create a new canonical memory; both sources transition to `expired` with `superseded_at` set | Partial overlaps that should be consolidated |
| `invalidate` | Mark the older memory as `expired` immediately; keep the newer one | A decision has changed and the old one is actively wrong |
| `relate` | Auto-add a typed relation between the two; both remain active | The memories are complementary, not contradictory |
| `suppress` | Record that this pair must not trigger future conflict warnings | Detected contradiction is a known acceptable coexistence |

Update `mem_resolve_conflict` MCP tool params: `obs_id_a`, `obs_id_b`, `action`
(add|merge|invalidate|relate|suppress), `merged_content` (required if action is
`merge`), `relation_type` (required if action is `relate`).

Update `ohara doctor` conflict reporting to show the five resolution options and their
recommended trigger conditions.

---

---

### 6.1 Git Sync Mode (JSONL Mirror)

#### Problem

Ohara's global SQLite does not travel with the repository. Switching machines or
sharing with a teammate loses all accumulated project memory.

Mulch solves this structurally. Ohara can achieve equivalent portability without
abandoning SQLite as the canonical store.

#### Design

SQLite remains source of truth. The JSONL mirror is a committable snapshot.

```
ohara sync export [--project <project>] [--output .ohara/]
ohara sync import [--from .ohara/] [--project <project>]
ohara sync status
```

**Export output structure:**

```
.ohara/
  decisions.jsonl
  patterns.jsonl
  bugfixes.jsonl
  learned.jsonl
  procedures.jsonl
  meta.json              # project key, export timestamp, ohara version
```

Each JSONL line: `obs_id`, `title`, `content`, `domain`, `kind`, `classification`,
`evidence_json`, `applies_to_json`, `related_json`, `created_at`, `updated_at`.
Revision history and session records excluded by default (`--include-history` flag
for full export).

**Git union merge strategy** (prevents conflicts between teammates):

```
# .gitattributes (auto-written by ohara sync export)
.ohara/*.jsonl merge=union
```

This instructs git to merge JSONL files by taking all unique lines from both sides,
which is safe for append-only records.

**Import behaviour**: Skip any record whose `obs_id` already exists locally. Surface
content conflicts between imported and local records as candidates for `mem_resolve_conflict`
rather than auto-merging.

**`ohara sync status`**: shows drift between DB and git mirror - how many memories
exist in DB but not in mirror, and vice versa.

**`.gitignore` recommendation** (document in README):

```gitignore
# Remove this line to share agent memory with the repo
.ohara/
```

**CI integration pattern:**

```yaml
- name: Restore Ohara memory
  run: ohara sync import --from .ohara/ --project ${{ github.repository }}
```

---

### 6.2 `procedure` Memory Type

#### Problem

Ohara's `pattern` type stores free-form descriptions of approaches. There is no
structured type for *executable, step-by-step workflows* - things the agent should
follow as a recipe.

Research (Voyager, Mem^p arXiv:2508.06433) consistently identifies procedural memory
as the highest-leverage type for coding agents: verified, structured procedures indexed
by a trigger condition, composed on demand.

#### New memory type: `procedure`

Add `procedure` as a valid value for the `kind` column. Default classification:
`foundational`.

Recommended content structure (stored in existing `content` column as JSON, or keep
free-form with only `trigger` as a dedicated column):

```json
{
  "trigger": "When adding a new Kubernetes Goat scenario",
  "preconditions": ["Cluster is running", "kg-scaffold is installed"],
  "steps": [
    "Add scenario ID to config/scenarios.yaml exclusion filter",
    "Run `kg-scaffold <id>` to generate the harness",
    "Add test coverage in test/scenarios/<id>_test.go",
    "Validate with `make test-kg`"
  ],
  "verified_at": "2025-11-15",
  "verified_by": "manual test run",
  "notes": "Step 3 can be skipped for read-only scenarios"
}
```

At minimum, add `trigger` as a dedicated indexed column for FTS5 searchability:

```sql
ALTER TABLE observations ADD COLUMN trigger_condition TEXT DEFAULT '';
```

`mem_search` should weight `trigger_condition` matches more heavily when the query
starts with "how to" or "when".

`mem_prime` should include a `Procedures` section (see section 4.3).

---

### 6.3 Relation Graph and `mem_related`

#### Problem

All memories are flat rows with no expressed relationships between them. If a `decision`
caused three `bugfix` entries, or a `procedure` implements a `decision`, that structure
is invisible. Agents must reconstruct it from text alone, which is unreliable at scale.

Note: for P0, `related_json` in the observations row (section 4.2) covers the basic
use case. This section is the upgrade to a proper queryable graph with traversal.

#### Schema addition

```sql
CREATE TABLE IF NOT EXISTS memory_relations (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  from_obs_id TEXT    NOT NULL REFERENCES observations(obs_id),
  to_obs_id   TEXT    NOT NULL REFERENCES observations(obs_id),
  relation    TEXT    NOT NULL
    CHECK(relation IN ('caused', 'resolves', 'supersedes', 'related_to', 'implements', 'contradicts')),
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(from_obs_id, to_obs_id, relation)
);

CREATE INDEX idx_relations_from ON memory_relations(from_obs_id);
CREATE INDEX idx_relations_to   ON memory_relations(to_obs_id);
```

#### Relation vocabulary

| Relation | Meaning |
|----------|---------|
| `caused` | This memory led to another |
| `resolves` | This memory fixes another |
| `supersedes` | This memory replaces another |
| `related_to` | General association |
| `implements` | A procedure implements a decision |
| `contradicts` | Explicit contradiction (mirrors conflict detection) |

#### New MCP tools

| Tool | Params | Description |
|------|--------|-------------|
| `mem_link` | `from_obs_id`, `to_obs_id`, `relation` | Create a typed relation |
| `mem_unlink` | `from_obs_id`, `to_obs_id`, `relation` | Remove a relation |
| `mem_related` | `obs_id`, `relation?`, `depth?` (default 1) | Traverse relations from a given memory |

When save-time conflict detection fires, automatically create a `contradicts` relation
in addition to the conflicts table entry so conflicts are traversable via `mem_related`.

---

### 6.4 Provider Setup Recipes

#### Problem

`ohara setup` currently only covers OpenCode. Mulch's `setup` command has idempotent
recipes per provider.

#### Deliverables

Extend `ohara setup <provider>` with recipes for:

- Claude Code (`~/.claude/settings.json` MCP block)
- Cursor (`.cursor/mcp.json`)
- Windsurf (`.windsurf/mcp.json`)
- Gemini CLI (`~/.gemini/settings.json`)
- VS Code Copilot (`.vscode/mcp.json`)

Add `ohara setup --check` to verify an integration is correctly configured, and
`ohara setup --remove <provider>` to cleanly undo it.

---

### 6.5 Sleep-Time Consolidation Worker

#### Problem

P3.1 consolidation is designed as an agent-invoked operation. This is correct from a
curation standpoint (agents must review and approve promotions), but the *heuristic
grouping* step - finding which episodic memories are candidates - does not require agent
involvement or LLM calls. Running it inside the active session loop adds latency and
consumes the session's reasoning budget.

When Ohara is the central memory layer across many active projects, session memories
accumulate faster than any single session can consolidate them. Without a background
worker, episodic noise builds up across all projects simultaneously. The next session
on any project is always starting from a noisier retrieval pool than the last.

Letta's sleep-time compute pattern is the right approach here: decouple the grouping
work from the conversation work. Background consolidation runs between sessions;
the next session picks up ready candidates without waiting and without the agent
spending tokens on housekeeping.

#### Design

Add a `--daemon` flag to `ohara consolidate`:

```
ohara consolidate [project] --daemon [--interval <minutes>] [--domain <domain>]
```

When running as a daemon (or via systemd/cron), the process:

1. Runs Mode A heuristic grouping on new episodic memories accumulated since the last
   consolidation sweep.
2. Writes `candidate` status memories to the DB (`status = 'candidate'`, `source =
   'consolidation'`). These are never returned by `mem_search` or `mem_prime` by default.
3. On next `mem_session_start`, if candidates exist, `mem_prime` appends a notice:
   `"Note: N consolidation candidates are ready for review. Call mem_consolidate_candidates."`
4. Agent reviews candidates, keeps or discards, calls `mem_mark_consolidated` on
   accepted ones.

No memory is ever auto-promoted. The daemon only produces candidates. Agent curation
remains mandatory before any candidate becomes `active`.

#### Status value addition

```sql
-- Migration 021: Candidate status for sleep-time consolidation (P2 - 6.5)
-- Requires modifying the status CHECK constraint to include 'candidate'.
-- SQLite requires recreating the table to change a CHECK constraint.
-- Recommended: implement status as an application-level enum validated in Go,
-- not in the DB constraint, for forward compatibility.
```

Add `candidate` to the valid `status` values in Go code. DB constraint change is
optional and can be deferred until a planned schema overhaul.

#### Systemd unit (optional, document in docs/)

```ini
[Unit]
Description=Ohara sleep-time consolidation worker
After=network.target

[Service]
ExecStart=/usr/local/bin/ohara consolidate --daemon --interval 60
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

---

Evaluate only after P0 and P1 are stable and generating real usage data.

---

### 7.1 Hierarchical Consolidation

#### Problem

Session memories accumulate as episodic records and are never abstracted into durable
project knowledge. Over time, session context crowds out higher-signal semantic memories
in retrieval. Research on CoALA and AgeMem (arXiv:2601.01885) identifies
episodic-to-semantic consolidation as one of the highest-leverage improvements for
long-running agents.

#### Design (agent-curated, never automatic)

Keep agent-curated writes as the default. Add an explicit consolidation job triggered
by the developer or agent.

```
ohara consolidate [project] [--domain <domain>] [--dry-run]
```

**Two modes:**

**Mode A - Heuristic (no LLM required):**
Group session memories by `domain + kind`. For each group, if the same subject noun
appears in three or more session memories with consistent resolution language, emit a
candidate semantic memory with `classification: "tactical"` and `status: "candidate"`.
Never auto-publish. The developer or agent reviews and promotes.

**Mode B - LLM-assisted (agent-driven):**
Expose `mem_consolidate_candidates` MCP tool that returns grouped raw episodes. The
calling agent produces a semantic summary and calls `mem_save` with `source: "consolidation"`.
The binary stays LLM-free; the agent does the synthesis.

#### Schema addition

```sql
ALTER TABLE observations ADD COLUMN source            TEXT DEFAULT 'agent'
  CHECK(source IN ('agent', 'consolidation', 'import'));
ALTER TABLE observations ADD COLUMN consolidated_from TEXT DEFAULT '';
-- comma-separated obs_ids that sourced this consolidation
```

#### New MCP tools

| Tool | Description |
|------|-------------|
| `mem_consolidate_candidates` | Returns grouped episodic memories ready for review |
| `mem_mark_consolidated` | Archives source episodic memories after a semantic memory is saved |

---

### 7.2 Hybrid Retrieval: FTS5 + Embeddings

#### Problem

FTS5 is BM25-equivalent: keyword frequency matching. A search for "failed auth
middleware" will miss "JWT verification race condition" despite being semantically
identical. This is the fundamental ceiling of keyword-only search.

Mem0 (arXiv:2504.19413) shows that adding vector retrieval alongside BM25 yields a 26%
relative improvement in recall accuracy.

#### Proposed implementation

FTS5 remains the default. Embedding sidecar is opt-in via config:

```yaml
retrieval:
  mode: hybrid                         # "fts5" (default) | "hybrid"
  embedding_backend: ollama
  embedding_model: nomic-embed-text
  embedding_dim: 768
  hybrid_alpha: 0.6                    # 0.0 = embedding only, 1.0 = FTS5 only
  ollama_url: http://localhost:11434
```

**Storage:**

```sql
CREATE TABLE IF NOT EXISTS obs_embeddings (
  obs_id     TEXT PRIMARY KEY REFERENCES observations(obs_id),
  embedding  BLOB NOT NULL,   -- float32 array as raw bytes
  model      TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**At save time**: If hybrid mode enabled, call ollama async after `mem_save` returns.
Do not block the save response.

**At query time**: Embed the query, compute cosine similarity, merge FTS5 and ANN
ranked lists via Reciprocal Rank Fusion (RRF), return top-K.

#### Zero-LLM-at-retrieval constraint

Graphiti achieves P95 retrieval latency of ~300ms with hybrid search by making zero
LLM inference calls during query processing. This must be an explicit design constraint
for Ohara's hybrid mode.

The constraint means:
- Query embedding computation via ollama (local model, acceptable latency) is allowed.
- BM25/FTS5 ranking is allowed.
- Cosine similarity via SIMD float32 dot product is allowed.
- RRF rank merging is allowed.
- Any LLM summarization, re-ranking, or synthesis call during `mem_search` is **not
  allowed** and belongs in a separate opt-in `mem_search_rerank` tool instead.

This keeps `mem_search` and `mem_context` latency deterministic and prevents the
embedding backend from becoming a bottleneck on the agent loop's critical path.

If a caller wants LLM-assisted re-ranking (e.g. score-then-read for top-3 of top-20
results), they call `mem_search_rerank` explicitly, which is documented as a slower,
optional second pass.

---

### 7.3 Temporal Knowledge Graph (Optional Index)

#### Problem

Graph/temporal approaches improve multi-hop and temporal reasoning but add operational
complexity. The recommendation is to implement this as an optional auxiliary index,
not default retrieval.

**Note on deferral:** Even in a multi-project, multi-agent deployment, the relation
graph from section 6.3 covers the practical cross-memory linking use cases. The
`entities` + `obs_entities` table design below is worth implementing only if
`mem_related` traversal proves insufficient for answering "which memories mention
service X" or "what decisions affect file Y" at scale. Do not implement before
the relation graph is in production and generating real usage data.

#### Design

Extract stable entities (service names, repositories, hosts, users, configs) from
`decision`, `procedure`, and `config` kinds only. Store edges with timestamps and
confidence.

```sql
CREATE TABLE IF NOT EXISTS entities (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  type        TEXT    NOT NULL,   -- "module", "file", "service", "concept"
  project_key TEXT    NOT NULL,
  UNIQUE(name, type, project_key)
);

CREATE TABLE IF NOT EXISTS obs_entities (
  obs_id    TEXT    REFERENCES observations(obs_id),
  entity_id INTEGER REFERENCES entities(id),
  PRIMARY KEY (obs_id, entity_id)
);
```

Entity extraction strategy:
- Regex-based for file paths and function signatures - no LLM needed.
- LLM-assisted for concept entities - optional, agent-driven via `mem_extract_entities`.

New MCP tool: `mem_graph_context` - given an entity name, return all memories that
reference it plus one hop of related memories via the relations table.

Only invoke TKG for explicit temporal questions ("when did X change?") or multi-hop
relationship queries ("which service depends on X?"). Keep FTS5 as default.

---

### 7.4 RL-Informed Memory Scoring

**Prerequisites:** 5.1 (temporal decay) and 5.2 (outcome tracking) must be stable.
Do not implement this on top of an untuned retrieval pipeline.

**Note on deferral:** The bandit update rule below is unlikely to be necessary even
at multi-project scale. The decay formula (5.1) combined with `outcome_boost` (5.2)
and `utility_weight` already gives four independent scoring signals. Adding a learned
weight on top of signals that are themselves being tuned adds instability and makes
retrieval quality harder to reason about. Revisit only if empirical benchmark data
from the section 7.5 harness shows a persistent gap that the hand-tuned formula
cannot close.

#### Concept

RL-informed memory (MemRL arXiv:2601.03192) learns *when to store, consolidate, and
forget* from observed retrieval utility. Instead of hand-tuned decay constants, the
scoring function is learned from outcomes.

#### Minimal implementation path

Add `utility_weight` to observations:

```sql
ALTER TABLE observations ADD COLUMN utility_weight REAL NOT NULL DEFAULT 1.0;
```

`mem_feedback` MCP tool: agent calls this after a task, passing `obs_id` list and
`helpful: true/false` per memory.

Update rule (bandit-style):

```
utility_weight += alpha * (reward - utility_weight)
-- where reward = 1.0 if helpful, 0.0 if not, and alpha = 0.1
```

Incorporate into relevance score from 5.1:

```
relevance_score = fts5_rank * decay_factor * log(1 + access_count)
                * outcome_boost * utility_weight
```

---

### 7.5 Evaluation Harness

Adopt measurable standards to avoid optimizing on intuition. Public benchmarks define
the acceptance criteria:

**LongMemEval competencies to test against:**
- Information extraction accuracy
- Multi-session reasoning
- Temporal reasoning (asof queries)
- Knowledge update correctness under contradictions
- Abstention rate (abstain vs. hallucinate)

**MemoryAgentBench competencies:**
- Retrieval precision@k
- Conflict false positive/negative rates
- Drift over time (does memory quality degrade as the DB grows?)
- Selective forgetting correctness under TTL and supersession

**Forgetting quality (new dimension from AgeMem / MemoryAgentBench 2026):**

Retrieving a correct memory is only half the contract. Forgetting stale or superseded
memories is equally important. Add three forgetting-quality metrics:

| Metric | Description | Measured by |
|--------|-------------|-------------|
| Stale recall rate | How often does a query return an expired/superseded memory that should have been pruned? | Seed expired memories with known supersession; verify they are absent from results |
| False forget rate | How often does decay/TTL remove a memory that would have been useful? | Track `utility_weight` at expiry time; flag if non-trivial weight was discarded |
| Conflict survival rate | After `mem_resolve_conflict` with `invalidate`, does the retired memory ever re-surface? | Explicit probe for invalidated `obs_id` in post-resolution search results |

Minimum viable forgetting harness: a Go test that seeds memories with known expiry
and supersession, runs queries at simulated future timestamps, and asserts that no
retired memory appears in results.

**Engineering deliverables:**

```
bench/
  longmemeval/    # evaluates retrieval hit rate, update correctness, abstention
  memoryagent/    # evaluates drift over time, forgetting correctness
  fixtures/       # sample memory sets with known ground truths
```

Minimum viable harness: a Go test binary that seeds a test DB, runs queries, and asserts
precision@k for a set of canonical queries. No LLM judge required for the first pass.

---

## 8. Security Requirements

Memory systems are susceptible to poisoning, prompt injection during augmentation steps,
and sensitive-data retention risks. These should be addressed in parallel with P1 work.

### Trust levels

Add `trust_level` to observations:

```sql
ALTER TABLE observations ADD COLUMN trust_level TEXT NOT NULL DEFAULT 'system'
  CHECK(trust_level IN ('user', 'system', 'tool', 'untrusted'));
```

Default deny writes from `untrusted` sources unless explicitly permitted in config.
Agents that write via MCP default to `tool`. Writes from the CLI default to `user`.

### Quarantine mode

New memories from agents can optionally be saved as `pending` status until reviewed:

```yaml
# ohara.config
write_policy:
  quarantine_kinds: ["decision", "procedure"]  # require approval before activating
  quarantine_trust: ["untrusted"]
```

### Audit log

Append-only log for save/update/delete operations with actor/session metadata:

```sql
CREATE TABLE IF NOT EXISTS audit_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  obs_id     TEXT    NOT NULL,
  action     TEXT    NOT NULL CHECK(action IN ('create', 'update', 'delete', 'archive')),
  actor_id   TEXT,
  session_id TEXT,
  trust_level TEXT,
  ts         DATETIME DEFAULT CURRENT_TIMESTAMP,
  snapshot   TEXT    -- JSON snapshot of the record before the action
);
```

### Redaction pipeline

Expand `stripPrivate()` (already referenced in the plugin spec) into a configurable
pre-write redaction step:

```yaml
# ohara.config
redaction:
  enabled: true
  patterns:
    - regex: 'ghp_[A-Za-z0-9]{36}'    # GitHub tokens
    - regex: 'sk-[A-Za-z0-9]{48}'     # OpenAI keys
  deny_kinds: []                        # refuse to save these kinds if secrets detected
```

### Provenance requirement

For `foundational` classification memories, require at least one evidence field before
the record transitions from `pending` to `active`. Configurable per kind.

---

## 9. Schema Migration Reference

All migrations are additive. No column drops, no table renames. Apply incrementally
with a versioned migration runner.

```sql
-- Bootstrap
CREATE TABLE IF NOT EXISTS schema_version (
  version    INTEGER PRIMARY KEY,
  applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Migration 001: Domain field (P0 - 4.1)
ALTER TABLE observations ADD COLUMN domain TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_obs_project_domain ON observations(project_key, domain);

-- Migration 002: Evidence and provenance (P0 - 4.2)
ALTER TABLE observations ADD COLUMN evidence_json   TEXT DEFAULT '{}';
ALTER TABLE observations ADD COLUMN applies_to_json TEXT DEFAULT '{}';
ALTER TABLE observations ADD COLUMN related_json    TEXT DEFAULT '{}';

-- Migration 003: Classification (P0 - 4.5)
ALTER TABLE observations ADD COLUMN classification TEXT NOT NULL DEFAULT 'tactical'
  CHECK(classification IN ('foundational', 'tactical', 'observational'));

-- Migration 004: Temporal decay (P1 - 5.1)
ALTER TABLE observations ADD COLUMN access_count  INTEGER  NOT NULL DEFAULT 0;
ALTER TABLE observations ADD COLUMN last_accessed DATETIME;

-- Migration 005: Usage tracking (P1 - 5.1)
CREATE TABLE IF NOT EXISTS memory_usage (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id TEXT    NOT NULL REFERENCES observations(obs_id),
  event     TEXT    NOT NULL CHECK(event IN ('retrieved', 'used')),
  session_id TEXT,
  ts        DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_usage_memory ON memory_usage(memory_id);

-- Migration 006: Outcome tracking (P1 - 5.2)
CREATE TABLE IF NOT EXISTS memory_outcomes (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id TEXT    NOT NULL REFERENCES observations(obs_id),
  status    TEXT    NOT NULL CHECK(status IN ('success', 'failure', 'unknown')),
  notes     TEXT,
  actor_id  TEXT,
  ts        DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_outcomes_memory ON memory_outcomes(memory_id);

-- Migration 007: Temporal fields (P1 - 5.5)
ALTER TABLE observations ADD COLUMN valid_from    DATETIME;
ALTER TABLE observations ADD COLUMN valid_to      DATETIME;
ALTER TABLE observations ADD COLUMN superseded_at DATETIME;
ALTER TABLE observations ADD COLUMN session_id    TEXT DEFAULT '';

-- Migration 008: Security (P1 - section 8)
ALTER TABLE observations ADD COLUMN trust_level TEXT NOT NULL DEFAULT 'system'
  CHECK(trust_level IN ('user', 'system', 'tool', 'untrusted'));
CREATE TABLE IF NOT EXISTS audit_log (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  obs_id      TEXT    NOT NULL,
  action      TEXT    NOT NULL CHECK(action IN ('create', 'update', 'delete', 'archive')),
  actor_id    TEXT,
  session_id  TEXT,
  trust_level TEXT,
  ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
  snapshot    TEXT
);

-- Migration 009: Procedure trigger field (P2 - 6.2)
ALTER TABLE observations ADD COLUMN trigger_condition TEXT DEFAULT '';

-- Migration 010: Relation graph (P2 - 6.3)
CREATE TABLE IF NOT EXISTS memory_relations (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  from_obs_id TEXT    NOT NULL REFERENCES observations(obs_id),
  to_obs_id   TEXT    NOT NULL REFERENCES observations(obs_id),
  relation    TEXT    NOT NULL
    CHECK(relation IN ('caused','resolves','supersedes','related_to','implements','contradicts')),
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(from_obs_id, to_obs_id, relation)
);
CREATE INDEX idx_relations_from ON memory_relations(from_obs_id);
CREATE INDEX idx_relations_to   ON memory_relations(to_obs_id);

-- Migration 011: Consolidation provenance (P3 - 7.1)
ALTER TABLE observations ADD COLUMN source            TEXT DEFAULT 'agent'
  CHECK(source IN ('agent', 'consolidation', 'import'));
ALTER TABLE observations ADD COLUMN consolidated_from TEXT DEFAULT '';

-- Migration 012: RL utility weight (P3 - 7.4)
ALTER TABLE observations ADD COLUMN utility_weight REAL NOT NULL DEFAULT 1.0;

-- Migration 013: Embedding sidecar (P3 - 7.2, optional)
CREATE TABLE IF NOT EXISTS obs_embeddings (
  obs_id     TEXT PRIMARY KEY REFERENCES observations(obs_id),
  embedding  BLOB NOT NULL,
  model      TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Migration 014: Entity graph (P3 - 7.3, optional)
CREATE TABLE IF NOT EXISTS entities (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  type        TEXT    NOT NULL,
  project_key TEXT    NOT NULL,
  UNIQUE(name, type, project_key)
);
CREATE TABLE IF NOT EXISTS obs_entities (
  obs_id    TEXT    REFERENCES observations(obs_id),
  entity_id INTEGER REFERENCES entities(id),
  PRIMARY KEY (obs_id, entity_id)
);
-- Migration 015: Bi-temporal model (P1 - 5.7)
ALTER TABLE observations ADD COLUMN ingested_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

-- Migration 016: Actor-aware writes (P1 - 5.8)
ALTER TABLE observations ADD COLUMN written_by TEXT NOT NULL DEFAULT 'agent'
  CHECK(written_by IN ('user', 'agent', 'consolidation', 'import', 'system'));

-- Migration 017: Store-time TTL (P1 - 5.9)
ALTER TABLE observations ADD COLUMN expires_at DATETIME;

-- Migration 018: Candidate status support (P2 - 6.5)
-- Status CHECK constraint update must be handled in Go application code
-- rather than SQLite to allow 'candidate' as a new valid status value.
-- No DDL migration needed; update the status enum in internal/store/types.go.

```

---

### Modified tools (all new params are optional - backward-compatible)

| Tool | Changes |
|------|---------|
| `mem_save` | Add: `domain`, `classification`, `evidence`, `applies_to`, `related`, `trigger` (procedure), `session_id`, `trust_level`, `written_by`, `expires_at` |
| `mem_search` | Add: `domain` filter, `file:` and `path:` filter prefixes, `min_confidence`, `asof:` / `since:` / `ingested_asof:` / `session:` operators, `written_by` filter; returns `relevance_score` per result and optional `conflicts` block |
| `mem_context` | Add: `domain` filter, `asof:` operator; returns optional `conflicts` block |
| `mem_stats` | Add per-domain, per-classification, and per-actor (`written_by`) breakdown |
| `mem_save_prompt` | Include domain, classification, procedure, evidence, actor, and TTL guidance |
| `mem_resolve_conflict` | Updated actions: `add`, `merge`, `invalidate`, `relate`, `suppress` (was: merge, link, suppress) |
| `mem_prime` | Add: `--show-actor` flag to tag injected memories by `written_by`; type-label each section entry |

### New tools

| Tool | Phase | Description |
|------|:-----:|-------------|
| `mem_list_domains` | P0 | List all distinct domains for a project |
| `mem_prime` | P0 | Return AI-optimised markdown context block for system prompt injection |
| `mem_append_outcome` | P1 | Record a success/failure outcome on a memory |
| `mem_mark_used` | P1 | Mark which retrieved memories were applied in a response |
| `mem_resolve_conflict` | P1 | Apply add/merge/invalidate/relate/suppress action on a conflict pair |
| `mem_forget` | P1 | Archive a memory with a documented reason; preserves audit trail |
| `mem_link` | P2 | Create a typed relation between two memories |
| `mem_unlink` | P2 | Remove a relation |
| `mem_related` | P2 | Traverse relations from a given obs_id to depth N |
| `mem_consolidate_candidates` | P3 | Return grouped episodic memories ready for consolidation review |
| `mem_mark_consolidated` | P3 | Archive source episodic memories after consolidation |
| `mem_graph_context` | P3 | Entity-based graph traversal query |
| `mem_extract_entities` | P3 | LLM-assisted entity extraction (agent-driven) |
| `mem_feedback` | P3 | Agent utility feedback for RL scoring |
| `mem_search_rerank` | P3 | Optional LLM-assisted re-ranking pass on top-N results from mem_search (slow path, explicit opt-in) |

---

## 11. Agent Instruction Updates (AGENTS.md)

Add these blocks as features ship.

### On domain (after 4.1)

```
Always set domain when saving a memory. Use the subsystem name in lowercase:
"auth", "database", "k8s", "api", "ci", "infra", "test". Keep domain strings
consistent across sessions. When searching, pass domain to narrow results if
you know the relevant subsystem.
```

### On evidence (after 4.2)

```
For decision and procedure memories, always set evidence with at least one of:
commit hash, issue ID, or file path. This makes the memory auditable and allows
file-targeted prime packs. For foundational memories, evidence is required before
the record is considered active.
```

### On classification (after 4.5)

```
Set classification when you have a strong signal: "foundational" for decisions and
procedures that should never auto-expire, "tactical" for patterns and bugfixes with
medium durability, "observational" for raw session notes. The default is "tactical".
Foundational memories are never auto-pruned.
```

### On procedure type (after 6.2)

```
Use kind "procedure" for step-by-step workflows you have personally verified work
in this project. Set trigger_condition to a "When X happens" phrase so future searches
can match on intent. Set evidence.verified_at. Update via mem_update on the original
obs_id rather than saving a duplicate when the procedure changes.
```

### On relations (after 6.3)

```
After saving a memory that directly relates to an existing one, call mem_link to
record the relationship. Common patterns:
  - A bugfix that resolves a known pattern:       relation "resolves"
  - A procedure that implements a decision:       relation "implements"
  - A new decision that replaces an old one:      relation "supersedes"
  - A decision that caused a later bug:           relation "caused"
```

### On actor-aware retrieval (after 5.8)

```
When a memory returned by mem_search or mem_prime is tagged [consolidation] or
[import], treat it as an inference rather than a directly verified fact. Confirm
against current environment state or a source memory before acting on it.
When calling mem_save, you do not need to set written_by - it is set automatically.
Do not override it unless you are importing from an external source.
```

### On forgetting (after 5.9)

```
When you identify a memory that is actively incorrect or has been superseded by a
new decision you are saving, call mem_forget on the old obs_id with a clear reason
and the replacement_obs_id of the new memory. Do not leave both active. Prefer
mem_forget over mem_delete: mem_forget preserves the audit trail.
When saving a memory that is temporary (a config change until next release, a
workaround until a bug is fixed), set expires_at to the date after which it stops
being relevant.
```

### On the four-choice conflict resolver (after 5.10)

```
When mem_resolve_conflict is needed, choose the action by this logic:
  - The two memories are about different things and both are correct: use 'add'
  - One partially subsumes the other: use 'merge' and write the canonical content
  - The older memory is actively wrong now: use 'invalidate'
  - The memories are complementary views of the same decision: use 'relate'
  - The detected contradiction is expected and acceptable: use 'suppress'
```

```
At the end of a significant project phase, call mem_consolidate_candidates to review
episodic session memories worth promoting to long-term knowledge. For each candidate
group, decide whether the pattern is durable enough to save as "learned" or "decision".
Call mem_mark_consolidated to archive the source records. Do not consolidate prematurely
- wait until the same pattern has appeared at least three times.
```

---

## 12. Definition of Done Checklists

### P0 DoD

- [ ] `domain` field exists on all memories; `mem_search` and `mem_context` accept domain filter.
- [ ] `evidence_json`, `applies_to_json`, `related_json` saved and returned; `file:` filter works.
- [ ] `classification` column exists with correct defaults per kind.
- [ ] `ohara prime` exists with token budget, kind filters, and file bias; outputs clean markdown.
- [ ] `ohara validate` fails hard on schema violations with non-zero exit code.
- [ ] `ohara doctor` identifies duplicates and stale procedures; `--fix` remediates safe items.

### P1 DoD

- [ ] `access_count` and `last_accessed` updated on every retrieval.
- [ ] `relevance_score` computed and returned per result; results sorted by it.
- [ ] `memory_outcomes` table exists; `mem_append_outcome` works; ranking incorporates outcome_boost.
- [ ] `mem_mark_used` records usage events in `memory_usage`.
- [ ] Retrieval-time conflict surfacing appends `conflicts` block when found.
- [ ] `mem_resolve_conflict` supports add/merge/invalidate/relate/suppress.
- [ ] `valid_from`, `valid_to`, `superseded_at`, `session_id` fields exist.
- [ ] `asof:` and `since:` retrieval operators work in `mem_search`.
- [ ] `ingested_at` field exists and is immutable after write; `ingested_asof:` operator works.
- [ ] `written_by` field exists; set correctly by write path; `mem_search` accepts `written_by` filter.
- [ ] `expires_at` field exists; background sweep transitions expired entries correctly.
- [ ] `mem_forget` tool exists; archives with reason; creates `supersedes` relation if replacement provided.
- [ ] `mem_resolve_conflict` updated to five-action vocabulary; `ohara doctor` documents trigger conditions.
- [ ] `min_confidence` param works; returns `low_confidence: true` on empty abstain.
- [ ] `trust_level` exists; `audit_log` records all create/update/delete/forget actions.
- [ ] Redaction pipeline runs pre-write and redacts configured patterns.

### P2 DoD

- [ ] `ohara setup` has recipes for Claude Code, Cursor, Windsurf, Gemini CLI, VS Code Copilot.
- [ ] `ohara sync export` writes `.ohara/*.jsonl` with correct `.gitattributes` union merge.
- [ ] `ohara sync import` imports without overwriting existing records; surfaces conflicts.
- [ ] `ohara sync status` shows DB vs mirror drift count.
- [ ] `procedure` kind exists; `trigger_condition` field is FTS5-indexed.
- [ ] `memory_relations` table exists; `mem_link`, `mem_unlink`, `mem_related` work.
- [ ] `ohara consolidate --daemon` runs Mode A heuristics in the background; writes `candidate` status memories.
- [ ] `mem_session_start` notifies agent when consolidation candidates are available.

### P3 DoD

- [ ] `mem_consolidate_candidates` and `mem_mark_consolidated` work end-to-end.
- [ ] Hybrid retrieval mode enabled via config; falls back to FTS5 if ollama unreachable.
- [ ] Hybrid retrieval makes zero LLM inference calls during `mem_search` or `mem_context`.
- [ ] `mem_search_rerank` exists as an explicit opt-in slow-path re-ranking tool.
- [ ] `bench/` directory exists with a runnable precision@k test harness.
- [ ] Forgetting quality harness exists: stale recall, false forget, and conflict survival tests pass.

---

## 13. Suggested Work Order

The order below reflects the multi-project, multi-agent deployment context. Items that
address retrieval noise across many projects and multi-agent write disambiguation are
promoted relative to a single-project use case.

**Tier 1 - Foundation (do first, zero latency cost, all additive schema)**
1. Schema migrations 001-003 (domain, evidence, classification) + update `mem_save`.
2. `ohara prime` CLI + `mem_prime` MCP tool.
3. `ohara validate` + `ohara doctor --fix`.

**Tier 2 - Retrieval quality and write governance**
4. Schema migrations 004-008 (decay, usage, outcomes, temporal, security).
5. Schema migrations 015-017 (bi-temporal `ingested_at`, `written_by`, `expires_at`).
6. Relevance scoring, `mem_mark_used`, `mem_append_outcome`, outcome_boost in ranking.
7. `written_by` assignment on all write paths; `written_by` filter in `mem_search`; `--show-actor` flag in `mem_prime`.
8. Retrieval-time conflict surfacing + `mem_resolve_conflict` (5-action vocabulary).
9. `mem_forget` tool + background TTL expiry sweep.
10. `asof:` / `since:` / `ingested_asof:` operators, `min_confidence` abstention.
11. Redaction pipeline and audit log wiring.

**Tier 3 - Portability and agent coverage**
12. Extended provider setup recipes (Claude Code, Cursor, Windsurf, Gemini CLI, VS Code Copilot).
13. Git sync mode (`ohara sync export|import|status`).
14. `procedure` kind + `trigger_condition` + `mem_prime` Procedures section.
15. Relation graph (`mem_link`, `mem_unlink`, `mem_related`).

**Tier 4 - Consolidation at scale**
16. Sleep-time consolidation worker (`ohara consolidate --daemon`).
17. Consolidation tooling (`mem_consolidate_candidates`, `mem_mark_consolidated`).

**Tier 5 - Do only when FTS5 ceiling is empirically hit**
18. Hybrid FTS5 + embeddings sidecar (zero-LLM-at-retrieval constraint enforced).
19. `mem_search_rerank` (explicit opt-in LLM re-ranking, separate from search).
20. Basic precision@k evaluation harness (`bench/`).

**Deferred - implement only with concrete evidence of need**
21. TKG entity graph (7.3) - implement only if `mem_related` graph proves insufficient for cross-memory entity queries at scale.
22. RL-informed bandit scoring (7.4) - implement only if empirical bench data shows the hand-tuned decay formula has a persistent gap.
23. Forgetting quality harness extensions (stale recall, false forget, conflict survival) - implement alongside 20 if forgetting correctness becomes a measurable concern.

---

*End of guidelines.*
