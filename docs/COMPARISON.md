# Comparison: Ohara vs Other Agent Memory Systems

[← Back to README](../README.md)

Last updated: 2026-06-23. Numbers are verified against the primary sources cited. For Ohara's own benchmark results, see [BENCHMARKS_RESULTS.md](BENCHMARKS_RESULTS.md).

## Multi-System Comparison

| | **Ohara** | **Mem0** | **Zep** | **Letta (MemGPT)** | **Hindsight** | **claude-mem** |
|---|---|---|---|---|---|---|
| **Architecture** | Single static Go binary | Python + vector DB | Java/Kotlin + Neo4j + vector DB | Python + vector DB | Python + Postgres | TypeScript + Python + ChromaDB |
| **Runtimes** | 1 process | 2+ (API + DB) | 3+ (API + Neo4j + vector DB) | 2+ | 2+ | Worker + ChromaDB |
| **Storage** | SQLite (single file) | Vector DB (Pinecone/Qdrant/Weaviate) | Neo4j + vector DB | Vector DB | Postgres + pgvector | SQLite + ChromaDB |
| **Search** | SQLite FTS5 + vec0 (ANN) | Vector ANN + graph | Neo4j Cypher + vector ANN | Vector ANN | Postgres + pgvector | ChromaDB vector |
| **Graph** | Typed relation graph (6 types) in SQLite | Neo4j knowledge graph | Neo4j temporal knowledge graph | None | None | None |
| **Temporal** | Bi-temporal (valid_from/valid_to) per-relation | None | Temporal knowledge graph (world + transaction time) | None | None | None |
| **LLM on hot path** | Zero (retrieval is pure SQL) | Yes (LLM reranking) | Optional | Yes (LLM-based recall) | Yes | Separate compression calls |
| **Embedding model** | External (Ollama, optional) | Built-in (OpenAI, optional) | Built-in | Built-in | Built-in | External (Claude API) |
| **Agent protocol** | MCP stdio — any MCP client | REST API | REST API + LangChain | REST API | REST API | Claude Code only |
| **Local-first** | Yes (single binary, offline-capable) | No | No | No | No | Semi (still needs ChromaDB) |
| **CGO required** | No (pure-Go modernc.org/sqlite) | N/A | N/A | N/A | N/A | Yes (ChromaDB deps) |
| **License** | MIT | Proprietary (with open-core) | Apache 2.0 | Apache 2.0 | Proprietary | AGPL-3.0 |
| **Install** | `go install` | `pip install` | Docker Compose | `pip install` | Docker Compose | Node.js + Bun + uv + Python |
| **Binary size** | ~13 MB stripped | N/A (Python) | N/A (JVM) | N/A (Python) | N/A (Python) | N/A (multi-runtime) |

## Benchmark Comparison

| | **Ohara** | **Mem0** | **Zep** | **Letta** | **Hindsight** |
|---|---|---|---|---|---|
| **LongMemEval** | 96.7† (30Q internal) / — (500Q) | 93.4 (LLM judge) | — | — | 91.4 |
| **LoCoMo** | — (harness ready) | 91.6 | — | — | — |
| **BEAM-1M** | — (harness ready) | 64.1 | — | — | — |
| **BEAM-10M** | blocked on scaling | 48.6 | — | — | 64.1 |
| **DMR** | — | — | 94.8% | 93.4% | — |

> † Ohara's 96.7 is on the internal 30-Q fixture with OverlapJudge (token-overlap), not the LLM judge used by Mem0. See [BENCHMARKS_RESULTS.md](BENCHMARKS_RESULTS.md) for judge caveats.
>
> Zep DMR: arXiv 2501.13956 (published Jan 2025, not 2026). Mem0: mem0.ai, June 2026. Hindsight: third-party reports; verify independently.
>
> One third-party report (agentry.press) cites Mem0 LongMemEval at 94.8; we report mem0.ai's own 93.4 and footnote the discrepancy.

## Design Philosophy

### Ohara

Curated memory: the agent decides what's worth remembering. The agent already has the LLM, context, and understands what just happened:

- `mem_save` after a bugfix: "Fixed N+1 query — added eager loading"
- `mem_session_summary` at session end: structured Goal/Discoveries/Accomplished/Files
- No noise from raw tool calls, no compression step, no extra API calls
- Works with any MCP client (Claude Code, OpenCode, Gemini CLI, Codex)
- Bi-temporal relation graph for "what did we believe at time T" queries
- Single binary: `go install` and you're done

### Mem0

Auto-capture: records all interactions automatically, then uses LLM to extract memories. Strong at scale with cloud vector DBs and graph-based enrichment. Locked to its own REST API and cloud infrastructure. Heavier operational footprint.

### Zep

Temporal knowledge graph: the pioneer of bi-temporal agent memory. Every fact carries both world-time (when it was true) and transaction-time (when the system learned it). Built on Neo4j + Cypher, which requires a second database daemon. Ohara's Phase 2 targets the same temporal capability in SQLite — same useful abstraction, zero new processes.

### Letta (MemGPT)

LLM-native memory: treats the agent's memory as a virtual context window managed by an LLM-based OS. Pioneered OS-style memory management for agents. Heavier LLM dependency even for basic retrieval.

### Hindsight

Postgres-based: uses PostgreSQL with pgvector for vector search. Reported BEAM-10M score of 64.1, the highest among all systems at that scale. Different tradeoff: requires a Postgres daemon.

### claude-mem

Raw capture: captures everything (all tool calls) and compresses with AI. Inspired Ohara's MCP approach but locked to Claude Code and requires multiple runtimes + ChromaDB. The inspiration for Ohara's MCP surface, but Ohara took the opposite design direction: curated over captured, single binary over multi-process.

## Sources

- [Mem0 2026 benchmarks](https://mem0.ai/blog/ai-memory-benchmarks-in-2026)
- [Zep / Graphiti: arXiv 2501.13956](https://arxiv.org/abs/2501.13956)
- [claude-mem GitHub](https://github.com/thedotmack/claude-mem)
- [Letta (MemGPT) GitHub](https://github.com/letta-ai/letta)
- [Hindsight GitHub](https://github.com/hindsight-ai/hindsight)
