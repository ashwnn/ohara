# Ohara Benchmark Results

Last updated: 2026-06-23. Numbers marked **internal fixture** use Ohara's built-in deterministic test fixtures and are not directly comparable to external systems. Numbers marked **official dataset** use the LongMemEval-S 500Q cleaned dataset.

## LongMemEval (Session-Distance Recall)

LongMemEval evaluates how well a memory system retains facts across increasing session distances. Facts are inserted in earlier sessions and questions are asked in later sessions, measuring recall degradation.

### Internal Fixture (30 questions, deterministic)

| Mode | Recall@1 | Recall@3 | MRR | nDCG@5 | P95 (ms) | Judge |
|------|----------|----------|-----|--------|----------|-------|
| FTS5 | 0.967 | 0.967 | 0.967 | 0.978 | < 10 | overlap |
| Hybrid (deterministic) | 0.933 | 0.967 | 0.950 | 0.977 | < 10 | overlap |

> **Note:** These numbers use Ohara's internal 30-question fixture and OverlapJudge (token-overlap evaluation), not the LLM judge used by Mem0. They reflect deterministic retrieval quality against a curated fixture, not the public 500-Q benchmark. Publish alongside the judge name.

### Official LongMemEval-S 500Q Dataset

| Mode | Recall@1 | Recall@3 | MRR | nDCG@5 | P95 (ms) | Judge |
|------|----------|----------|-----|--------|----------|-------|
| FTS5 | — | — | — | — | — | containment |
| Hybrid (deterministic) | — | — | — | — | — | containment |

> **To fill:** Run `./bench/run-benchmark-build.sh` with the official dataset at `bench/longmemeval/data/longmemeval_s_cleaned.json`. The harness auto-detects JSON array vs JSONL format and uses ContainmentJudge by default for the official dataset.

### External comparison (source: mem0.ai, June 2026)

| System | LongMemEval Score | Judge |
|--------|-------------------|-------|
| **Mem0** | 93.4 | LLM (GPT-4o) |
| **Hindsight** | 91.4 | LLM |
| **Ohara** (internal 30Q, OverlapJudge) | 96.7 | token-overlap |
| **Ohara** (official 500Q, ContainmentJudge) | — | containment |

> **Caveat:** Ohara's 96.7 is on the internal 30-Q fixture with OverlapJudge, which is simpler than the LLM judge used by Mem0. The official 500-Q number (to be produced) uses ContainmentJudge, which measures how completely expected answers appear in retrieved bodies without penalizing long transcripts. Neither is directly comparable to Mem0's LLM-judged 93.4. Always name the judge.

> One third-party report (agentry.press) cites Mem0 LongMemEval at 94.8; we report mem0.ai's own 93.4 and footnote the discrepancy.

## LoCoMo (Long-Context Memory)

LoCoMo evaluates memory across long conversations (~600 turns, ~26k tokens) with single-hop, multi-hop, temporal, and open-domain questions.

### Internal Fixture (2 conversations, 10 questions, deterministic)

| Mode | Recall@1 | Recall@3 | MRR | nDCG@5 | P95 (ms) | Passed/Total |
|------|----------|----------|-----|--------|----------|-------------|
| FTS5 | 0.500 | 0.600 | 0.583 | 0.577 | < 50 | 6/10 |
| Hybrid (deterministic) | 0.500 | 0.600 | 0.583 | 0.577 | < 50 | 6/10 |

> **Note:** These are internal fixture numbers. Official LoCoMo dataset integration requires dataset acquisition. The harness (`bench/locomo/`) and runner (`bench/cmd/run-locomo/`) are ready for external dataset import via `ImportFromJSON()`.

### External comparison (source: mem0.ai, June 2026)

| System | LoCoMo Score |
|--------|-------------|
| **Mem0** | 91.6 |
| Full-context baseline | ~73 |
| Best RAG baseline | ~61 |
| **Ohara** (internal fixture) | 60.0 (Recall@3) |

> **Caveat:** The Ohara internal fixture is a 10-question deterministic test, not the full LoCoMo benchmark. The 60.0 number is a lower bound on a small fixture; the official dataset would produce a higher and more representative number.

## BEAM-1M (Benchmark for Episodic Agent Memory)

BEAM evaluates agent memory at scale across multiple conversation-scoped probe types: fact retrieval, entity linking, contradiction detection, temporal ordering, instruction-vs-preference, multi-hop reasoning, and summarization.

### Internal Fixture (2 conversations, 10 probes, deterministic)

| Mode | Recall@1 | Recall@3 | MRR | nDCG@5 | P95 (ms) | Passed/Total |
|------|----------|----------|-----|--------|----------|-------------|
| FTS5 | 0.300 | 0.300 | 0.200 | 0.262 | < 50 | 3/10 |
| Hybrid (deterministic) | 0.300 | 0.400 | 0.325 | 0.351 | < 50 | 4/10 |

> **Note:** These are internal fixture numbers. BEAM-1M requires the official dataset (up to 100 conversations, ~1M tokens). The harness (`bench/beam/`) and runner (`bench/cmd/run-beam/`) are ready for external dataset import via `ImportFromJSON()`.

### External comparison (source: mem0.ai, June 2026)

| System | BEAM-1M Score |
|--------|--------------|
| **Mem0** | 64.1 |
| **Hindsight** | — (BEAM-10M: 64.1) |
| **Ohara** (internal fixture) | 30.0 (Recall@3) |

> **Caveat:** The Ohara internal fixture is a 10-probe deterministic test, not the full BEAM-1M benchmark. The 30.0 number is a lower bound on a small fixture. BEAM-10M is a separate scaling effort gated on Phase 1 latency at extreme N.

## Retrieval Quality (Internal Fixture)

Ohara's internal retrieval fixture tests 70+ cases across lexical matching, graph context, file context, and packing.

| Mode | Recall@3 | MRR | nDCG@5 | P95 (ms) |
|------|----------|-----|--------|----------|
| FTS5 | ≥ 0.80 | ≥ 0.70 | — | ≤ 50 |
| Hybrid (Ollama) | ≥ 0.80 | ≥ 0.70 | — | ≤ 50 |

> **SLO gates** (enforced in CI via `run-retrieval -enforce`): Recall@3 ≥ 0.80, MRR ≥ 0.70, p95 ≤ 50ms, abstention FP ≤ 0.10.

## Running locally

```bash
# LongMemEval fixture gate (30-Q, enforced SLOs)
go run ./bench/cmd/run-longmemeval/ -k 5 -enforce -skip-latency

# LongMemEval sweep (compare modes)
go run ./bench/cmd/run-longmemeval/ -k 5 -sweep

# LoCoMo fixture gate
go run ./bench/cmd/run-locomo/ -k 5 -enforce -skip-latency

# BEAM fixture gate
go run ./bench/cmd/run-beam/ -k 5 -enforce -skip-latency

# Full benchmark build (requires official dataset)
OHARA_LONGMEMEVAL_DATASET=bench/longmemeval/data/longmemeval_s_cleaned.json \
  ./bench/run-benchmark-build.sh
```

## Sources

- Mem0 2026 benchmarks: [mem0.ai/blog/ai-memory-benchmarks-in-2026](https://mem0.ai/blog/ai-memory-benchmarks-in-2026)
- Zep / Graphiti: [arXiv 2501.13956](https://arxiv.org/abs/2501.13956)
- LongMemEval dataset: [HuggingFace](https://huggingface.co/datasets/TIGER-Lab/LongMemEval)
- BEAM benchmark: [GitHub](https://github.com/mem0ai/beam)
