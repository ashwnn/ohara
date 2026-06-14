# Bench Coverage Matrix (T9 Memo)

Automated at commit `72a5cb3`. All harnesses deterministic, no CGO, pure Go + SQLite FTS5.

## Harness Inventory

| Harness | Package | Type | Tests | Metrics |
|---------|---------|------|-------|---------|
| Retrieval Fixture | `bench/retrieval/` | Go test + runner | 19 tests | Recall@1/3/5, MRR, nDCG@5, stale/superseded/wrong-project hit rates, file-context accuracy, graph-context accuracy, pack budget compliance, abstention FP, latency SLO |
| LongMemEval | `bench/longmemeval/` | Go test + runner | 25 tests | Recall@1/3/5, MRR, nDCG@5, distance-stratified (near/medium/far), judge score, latency SLO |
| Forgetting Quality | `bench/forgetting/` | Go test | 3 tests | Stale recall prevention, false forget prevention, conflict survival |
| Quality Report | `bench/quality/` | Go test | 9 tests | MRR@5, Recall@3/10, conflict detection, staleness, access count, actor isolation, temporal decay, auto-archive, dedup |
| Precision@k | `bench/precision/` | Go run | manual | precision@k (3 queries) |
| Store Performance | `bench/store/` | Go benchmark | benchmarks | Save throughput, search latency, pack build, DB size |

## Coverage by Quality Dimension

| Dimension | Retrieval | LongMemEval | Forgetting | Quality | Precision |
|-----------|-----------|-------------|------------|---------|-----------|
| Recall@k | ✓ | ✓ | - | ✓ | ✓ |
| MRR | ✓ | ✓ | - | ✓ | - |
| nDCG@5 | ✓ | ✓ | - | - | - |
| Stale/Archived filtering | ✓ | - | ✓ | ✓ | - |
| Wrong-project isolation | ✓ | - | - | - | - |
| Superseded filtering | ✓ | - | - | - | - |
| File-context accuracy | ✓ | - | - | - | - |
| Graph-context accuracy | ✓ | - | - | - | - |
| Pack budget compliance | ✓ | - | - | - | - |
| Abstention FP rate | ✓ | - | - | - | - |
| Session-distance recall | - | ✓ | - | - | - |
| Judge-based scoring | - | ✓ | - | - | - |
| Hybrid/embedding mode | ✓ | ✓ | - | - | - |
| False forget prevention | - | - | ✓ | - | - |
| Conflict survival | - | - | ✓ | ✓ | - |
| Temporal decay | - | - | - | ✓ | - |
| Access frequency boost | - | - | - | ✓ | - |
| Deduplication | - | - | - | ✓ | - |
| Latency SLO | ✓ | ✓ | - | - | - |
| JSON replay output | ✓ | ✓ | - | - | - |
| Fixture audit | ✓ | - | - | - | - |
| FTS5 fallback verification | ✓ | ✓ | - | - | - |

## Threshold SLO Gates (Enforced)

| Gate | Retrieval | LongMemEval |
|------|-----------|-------------|
| Overall Recall@3 | ≥ 0.80 | ≥ 0.75 |
| Lexical Recall@3 | ≥ 0.90 | - |
| File-Aware Recall@3 | ≥ 0.85 | - |
| Near Recall@3 | - | ≥ 0.80 |
| Medium Recall@3 | - | ≥ 0.70 |
| Far Recall@3 | - | ≥ 0.60 |
| MRR | ≥ 0.70 | ≥ 0.65 |
| Stale Hit Rate | ≤ 0.0 | - |
| Wrong-Project Hit Rate | ≤ 0.0 | - |
| Latency p95 | ≤ 50ms | ≤ 100ms |
| Latency max | ≤ 150ms | ≤ 500ms |
| Pack Budget Compliance | = 1.0 | - |
| Abstention FP | ≤ 0.10 | - |

## Remaining Gaps (from OHARA_IMPROVEMENT_GUIDELINES_v3.md)

All items from Tiers 1-2 implemented. All items from Tier 3 (portability/coverage) implemented except:
- Item 12: Provider setup recipes (Claude Code, Cursor, Windsurf, Gemini CLI, VS Code Copilot) — partially done via setup.sh
- Item 13: Git sync mode — implemented as `ohara sync`

All items from Tier 4 (consolidation) implemented.

Tier 5 items (do only when FTS5 ceiling hit):
- Item 18: Hybrid FTS5 + embeddings sidecar — **DONE** (internal/store/hybrid.go)
- Item 19: mem_search_rerank — **DONE** (explicit opt-in MCP tool)
- Item 20: Basic precision@k evaluation harness — **DONE** (bench/precision/, bench/retrieval/, bench/longmemeval/)

Deferred items:
- Item 21: TKG entity graph — **Deferred** (no evidence of need)
- Item 22: RL-informed bandit scoring — **Deferred** (no empirical gap)
- Item 23: Forgetting quality harness — **DONE** (bench/forgetting/)

## Blockers for Full LongMemEval Integration

| Blocker | Mitigation |
|---------|------------|
| Public LongMemEval dataset requires HuggingFace access | JSONL importer (ImportFromJSONL) ready; offline fixture fallback documented |
| Judge model for answer-level evaluation (not just fact retrieval) | OverlapJudge baseline implemented; LLM judge scaffold defined via JudgeModel interface |
| Full dataset contains 1000+ records | Importer supports streaming JSONL with 1MB line buffer; tested with partial-error handling |

## Next Steps (T2+)

1. Download LongMemEval dataset from HuggingFace, run through ImportFromJSONL, measure Ohara recall
2. Implement LLM-based JudgeModel for semantic answer evaluation
3. Add more distractor patterns (cross-domain noise, temporal confusion facts)
4. Profile and optimize for large dataset runs (1000+ facts)
