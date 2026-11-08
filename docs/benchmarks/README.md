# Benchmarks

Numbers come **only from real runs**. This directory records each run's
methodology so numbers can be reproduced — and judged for what they don't show.

Harness: `test/benchmark/workload_test.go` (go test/bench) + edge-case
workplace `test/benchmark/edge_workload_test.go` with runner
`test/benchmark/edge.sh`.

## Honest rerun (2026-11-08, reflection loop Iteration 1)

The prior harness reused the *same* payload every iteration, so iterations
2..1000 hit the chunkstore dedup fast path (Pebble Get, ~0.02 ms) instead of
the Put+fsync path. Reported 21,865 ops/s was therefore a dedup-cache number,
not a quorum-fsync number. The harness now uses fresh payloads per iteration
(counter bytes in the first 8 bytes), forcing real fsyncs.

- Host: darwin/arm64 Apple M4, Go 1.26.0, `t.TempDir()` stores, in-process
  fan-out (no QUIC, no FUSE, no NVMe/10 GbE claim).
- Commands:
  - `go test ./test/benchmark/... -run TestEdge -v -count=1 -race` → gate PASS (C1–C8).
  - `go test ./test/benchmark/... -run XXX -bench 'Edge' -benchtime=100x -count=1`
  - `go test ./test/benchmark/... -run XXX -bench BenchmarkWorkloadQuorumWrite -benchtime=100x -count=1`

| Metric | Dedup-inflated (before) | Fresh-payload (now) |
| --- | --- | --- |
| Quorum write 4 KiB, 3 voters | 21,865 ops/s, p50 0.021 ms | **~42–44 ops/s, p50 ~23 ms, p99 ~29–32 ms, max ~43 ms** |
| Metadata dispatch (create+rename) | — | 61.7 µs/op (in-memory) |
| Invalid-name reject | — | 8.5 µs/op (fail-fast, no panic) |
| Atomic batch reject | — | 1.6 µs/op (pre-validation, no writes) |

The ~400x correction is the honest number for this environment: fsync on
local temp dirs over 3 in-process stores. Do not compare to the §11 gate
(1,000 ops/s, p99 ≤ 25 ms on 10 GbE + NVMe) — different machine, different path.

Cross-check (same harness, Docker ARM64 Linux `node-a`, 2026-11-08):
fresh-payload ~823 ops/s, p50 ~1.17 ms — faster local fs in the VM, still
NOT reference hardware and NOT the §11 gate.

Raw JSON: `docs/benchmarks/results.json`.
Data table + gate tracker: `docs/benchmarks/metrics-table.md`.

## What this does NOT show

- Nothing about the §11 qualification gates (p99 ≤ 25ms at 1000 ops/s on
  10 GbE + NVMe, `fsync` honor) — different environment, different path
  (in-process, no FUSE, no QUIC, no network).
- Nothing about strict-mode reads (ReadIndex + leases) or group commit.
- Nothing about target-application behavior (M5-02 suite).

No conclusion is drawn from these numbers beyond "the edge gate passes and
the fsync path costs ~23 ms at p50 on this laptop".
