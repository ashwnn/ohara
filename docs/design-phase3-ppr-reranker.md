# Design Note: Graph-Aware Personalized PageRank Reranker

**Phase 3 item:** Narrow PPR reranker  
**Date:** 2026-06-23  
**Status:** Design approved — awaiting implementation  

## 1. Motivation

### Evidence from BEAM baseline (measured 2026-06-23 against `bench/beam/fixture.json`)

| Probe Type | Recall@3 (fts5) | Recall@3 (hybrid) | Probes |
|-----------|-----------------|-------------------|--------|
| fact_retrieval | 0.333 | 0.333 | 6 |
| temporal_order | 0.500 | 0.500 | 2 |
| **multi_hop** | **0.000** | **0.000** | 2 |

Multi-hop probes completely fail (0/2). Example failure, p-009:

> "What vector solution was suggested and what is the constraint that affects it?"
> Expected: conv-001:msg:3 (vec0 suggestion) + conv-001:msg:4 (version constraint)
> Actual: empty result (no expected facts in top-5)

The underlying facts *are* in the store (`conv-001:msg:3` = "modernc.org/sqlite gives you pure-Go SQLite. For vectors, the vec0 extension is available now."; `conv-001:msg:4` = "The vec0 extension is in modernc v1.47.0+. But we need to stay on v1.45.0 for now."). Lexical search alone cannot connect them because the query does not contain the keywords "vec0", "modernc", or "v1.47.0".

The relation graph already captures the semantic relationships:
- `conv-001:msg:3` and `conv-001:msg:4` are temporally adjacent in the same conversation
- They share the entity "vec0" (extractable via `ExtractEntitiesHeuristic`)
- They are linked by `related_to` / `caused` relations (if captured)

A graph-aware reranker can propagate relevance from the few nodes that match the query lexically to their graph neighbors, surfacing multi-hop answers.

### Runner-up comparison

- **Sleep-time consolidation** (Letta-style offline merge): large surface area (~4-5 days), depends on Ollama, only runs offline. Useful but lower ROI given the current zero-hot-path-LLM differentiation.
- **Pluggable HNSW**: superseded by `vec0` (Phase 1, already integrated). No evidence of need above `vec0`.

PPR reranker is the tightest-scope, highest-evidence next item.

## 2. What It Is

A **post-retrieval reranker** that takes the RRF-fused candidate list (currently ~top-N items from `fuseHybridRRF`), builds a small in-memory adjacency graph from `memory_relations`, runs a bounded Personalized PageRank from query-relevant seed nodes, and reorders candidates by a linear blend of PPR score × lexical/vector relevance.

**Scope:** Narrow. This is NOT a full HippoRAG port. No embedding-based graph construction, no LLM-based relation extraction, no offline graph index. It uses only the existing `memory_relations` table (6 typed relations) and the deterministic entity extractor. It runs entirely in-Go, in-process, with no new dependencies.

## 3. Constraint Compliance

| Constraint | Compliance |
|-----------|-----------|
| Single static Go binary | ✅ Pure-Go matrix operations, no new deps |
| No CGO | ✅ No external libraries |
| No second daemon | ✅ In-process after existing retrieval |
| Zero LLM on hot path | ✅ Deterministic PPR on existing relation graph |
| Binary size ≤ ~5 MB increase | ✅ ~200-400 LOC, no new imports |
| SLO gates pass | ✅ Retains existing scores; PPR boost is additive, gated by flag |

## 4. Algorithm Sketch

```
Input:  query string, RRF candidate list C (len ≤ ~200)
Output: reordered candidate list C'

1. Extract query entities via ExtractEntitiesHeuristic(query) → seeds
2. Load relations for all candidates: memory_relations where from_obs_id OR to_obs_id in C.ids
3. Build adjacency matrix A[n×n] over candidate IDs:
   - edge weight = relationTypeWeight(rel) + entityCooccurrenceBoost
   - self-loops excluded
4. Build personalization vector p[n]:
   - pi = 1.0 if candidate i directly matches any seed entity (via obs_entities)
   - pi = 0.5 if candidate i matches query terms lexically (FTS5 rank > 0)
   - pi = 0.1 otherwise (uniform fallback)
5. Run bounded PPR (≤ 5 iterations, teleport α=0.15):
   s ← (1-α) × Â × s + α × p   (Â = row-normalized A)
6. Blend: final_score[i] = (1-λ) × original_score[i] + λ × s[i]
   - λ = 0.15 by default (tunable per query type)
7. Re-sort by final_score, return top-K
```

### Key design decisions

- **Narrow candidates:** Only build graph over the top ~200 RRF candidates, not the entire DB. Keeps matrix ops O(200²), not O(N²).
- **Teleport α=0.15:** Standard PPR damping; prevents rank sink.
- **5 iterations:** Sufficient for convergence on small graphs; measured empirically.
- **No SVD/PCA:** Full PageRank, not a low-rank approximation. Graph is small enough.
- **Flag-gated:** Controlled by `--ppr-rerank` or config field; off by default to preserve current behavior for users not needing multi-hop.

## 5. Integration Point

```
mem_search / mem_pack
  └─ fuseHybridRRF() or SearchMemories()
       └─ returns candidate list (ranked by RRF)
            └─ [NEW] pprRerank(candidates, query)  ← insert here
                 └─ returns reordered candidates
                      └─ existing limit truncation
```

File: `internal/store/hybrid.go`, new function `pprRerank()` called after `fuseHybridRRF` returns, before truncation to top-K.

## 6. Implementation Slices

### Slice 1: Core PPR engine (new file: `internal/store/ppr.go`)

**What**: Pure-Go PPR implementation: graph construction from `memory_relations`, personalization vector from entities + lexical matches, iterative PPR, blend-and-resort.

**Functions:**
- `buildPPRGraph(candidateIDs []int64, relations []Relation) pprGraph`
- `buildPersonalizationVector(graph, candidateIDs, seedEntities, lexicalMatches) []float64`
- `personalizedPageRank(graph, personalization, α=0.15, iters=5) []float64`
- `blendAndRerank(candidates, pprScores, λ=0.15) []scoredPackCandidate`

**Verification:** Unit tests with small synthetic graphs; verify rank consistency on 5-node and 10-node fixtures.

### Slice 2: Wire into retrieval pipeline (`internal/store/hybrid.go`)

**What**: Add `pprRerank()` call after `fuseHybridRRF()` in the hybrid search path. Add `PPRRerank` bool to store config.

**Verification:** BEAM multi-hop probes improve (p-009, p-010 should pass). Existing retrieval fixture scores unchanged (or within noise). SLO gates pass.

### Slice 3: BEAM fixture expansion + CI gate

**What**: Expand BEAM fixture to include more multi-hop and temporal probes (from 10 to ~25 probes, with 8+ multi-hop). Add CI gate that tracks multi-hop recall@3.

**Verification:** `go test ./bench/beam/` green; CI job reports multi-hop Recall@3.

### Slice 4 (optional): Temporal walk variant

**What**: If temporal_order probes don't improve from PPR alone, add a temporal-weighted walk variant that edges weight by temporal proximity (adjacent turns in same conversation get higher weight).

**Verification:** Temporal BEAM probes improve.

## 7. Risk Assessment

| Risk | Mitigation |
|------|-----------|
| PPR degrades single-hop recall | Flag-gated; off by default; benchmark both modes |
| Matrix ops too slow at scale | N=200 limit; 200²×5 iters ≈ 200k FLOPs, negligible |
| Entity extraction too noisy | Seed personalization uses lexical fallback; PPR is forgiving |
| New dependency on relation graph completeness | Works with sparse graphs; personalization vector carries lexical signal |
| Binary size increase | Pure-Go, ~200-400 LOC, no new imports |

## 8. Measurement Baseline (2026-06-23)

| Metric | Value |
|--------|-------|
| BEAM multi_hop Recall@3 | 0.000 (0/2) |
| BEAM temporal_order Recall@3 | 0.500 (1/2) |
| BEAM fact_retrieval Recall@3 | 0.333 (2/6) |
| Retrieval fixture Recall@3 | 0.966 |
| Binary size (stripped) | 13.7 MB |
| vec0 status | Integrated (768d, >1000 row threshold) |
| Relation graph types | 6 (caused, resolves, supersedes, related_to, implements, contradicts) |
| Entity extraction | Deterministic (ExtractEntitiesHeuristic) |

## 9. Success Criteria

- BEAM multi-hop Recall@3 improves from 0.000 to ≥ 0.40 on expanded fixture
- Retrieval fixture Recall@3 ≥ 0.95 (no regression)
- SLO gates: Recall@3 ≥ 0.80, MRR ≥ 0.70, p95 ≤ 50ms, abstention FP ≤ 0.10
- Binary size delta ≤ 500 KB
- No new direct dependencies in `go.mod`
