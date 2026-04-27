#!/bin/bash
# run.sh — Run the Ohara store benchmark suite with nice formatting.
set -e

cd "$(dirname "$0")"

echo "========================================================"
echo "  Ohara Store Benchmark Suite"
echo "  $(date)"
echo "========================================================"
echo ""

# Run with 3s per benchmark for statistical significance
# Use -csv for machine-readable output if available, otherwise plain text
go test ./bench/store/ -bench=. -benchmem -benchtime=3s 2>&1

echo ""
echo "========================================================"
echo "  Run complete"
echo "========================================================"