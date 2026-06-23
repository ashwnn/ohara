#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmp_root="${TMPDIR:-/tmp}/ohara-benchmark-build"
mkdir -p "$tmp_root/go-cache" "$tmp_root/go-tmp" "$tmp_root/bin"

export GOCACHE="${GOCACHE:-$tmp_root/go-cache}"
export GOTMPDIR="${GOTMPDIR:-$tmp_root/go-tmp}"

dataset="${OHARA_LONGMEMEVAL_DATASET:-bench/longmemeval/data/longmemeval_s_cleaned.json}"
version="${VERSION:-benchmark}"
binary_out="${OHARA_BENCHMARK_BINARY_OUT:-$tmp_root/bin/ohara}"

if [[ ! -f "$dataset" ]]; then
  echo "missing LongMemEval dataset: $dataset" >&2
  echo "set OHARA_LONGMEMEVAL_DATASET or download bench/longmemeval/data/longmemeval_s_cleaned.json" >&2
  exit 1
fi

question_flags=()
if [[ -n "${OHARA_LONGMEMEVAL_QUESTIONS_LIMIT:-}" ]]; then
  question_flags=(-questions-limit "${OHARA_LONGMEMEVAL_QUESTIONS_LIMIT}")
fi

worker_flags=()
if [[ -n "${OHARA_LONGMEMEVAL_WORKERS:-}" ]]; then
  worker_flags=(-workers "${OHARA_LONGMEMEVAL_WORKERS}")
fi

echo "========================================================"
echo "  Ohara Benchmark Build"
echo "  $(date)"
echo "  Dataset: $dataset"
echo "========================================================"
echo ""

echo "[1/4] Building stripped benchmark binary"
go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$binary_out" ./cmd/ohara

echo ""
echo "[2/4] LongMemEval fixture gate (fts5)"
go run ./bench/cmd/run-longmemeval/ -k 5 -fixture bench/longmemeval/fixture.json -enforce -skip-latency "${worker_flags[@]:-}" "${question_flags[@]:-}"

echo ""
echo "[3/4] LongMemEval official 500Q (fts5, report-only)"
go run ./bench/cmd/run-longmemeval/ -k 5 -dataset "$dataset" -mode fts5 -enforce=false -skip-latency "${worker_flags[@]:-}" "${question_flags[@]:-}"

echo ""
echo "[4/4] LongMemEval official 500Q (hybrid deterministic, report-only)"
go run ./bench/cmd/run-longmemeval/ -k 5 -dataset "$dataset" -mode hybrid -enforce=false -skip-latency "${worker_flags[@]:-}" "${question_flags[@]:-}"

echo ""
echo "========================================================"
echo "  Benchmark build complete"
echo "  Binary: $binary_out"
echo "========================================================"
