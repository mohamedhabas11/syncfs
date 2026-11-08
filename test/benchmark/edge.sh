#!/usr/bin/env bash
# Edge-case workload script for SyncFS (maturity-gate artifact).
# Runs the C1-C8 edge correctness gate + fresh-payload throughput benches.
# Exit non-zero on any failure. Numbers are in-process only (no QUIC/FUSE/
# NVMe) — see docs/benchmarks/README.md.
set -euo pipefail
cd "$(dirname "$0")/../.."
# Prefer the container fleet (Go 1.26); fall back to local Go 1.26 via SYNCFS_GO.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  RUN="docker compose run --rm node-a"
else
  GO="${SYNCFS_GO:-go}"
  RUN="$GO"
  echo "warning: docker not found; using $GO (must be 1.26)" >&2
fi
echo "==> build"
$RUN go build ./...
echo "==> vet"
$RUN go vet ./...
echo "==> edge gate (correctness)"
$RUN go test ./test/benchmark/... -run TestEdge -v -count=1 -race
echo "==> full shipped suite"
$RUN go test ./... -count=1 -race
echo "==> edge benchmarks (fresh payloads, benchtime=100x)"
$RUN go test ./test/benchmark/... -run XXX -bench 'Edge|BenchmarkWorkloadQuorumWrite' -benchtime=100x -count=1 -v
