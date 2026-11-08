# Benchmarks

Numbers come **only from real runs**. This directory records each run's
methodology so numbers can be reproduced — and judged for what they don't show.

Harness: `test/benchmark/workload_test.go` (go test/bench). Workloads: five
(edge-case) paths — quorum 4 KiB write + fsync + read, rename pressure, lock
pressure, cache — wired against 3 in-memory voters.

## Methodology (run 2026-11-08)

- Commit: `docs/benchmarks/results.json` records the tree's HEAD at run time.
- Container: `syncfs-node-a` (Docker, ARM64 Linux, Go 1.26.10), stores on
  host TMP volumes — NOT reference hardware.
- Network: in-process fan-out (no QUIC). No 10 GbE involvement.
- Storage: local ext4-ish FS in the dockers VM; no PLP/flush hardware claim.
- Command: `docker compose run --rm node-a go test ./test/benchmark/... -count=1 -v -bench BenchmarkWorkloadQuorumWrite -benchtime=1000x`

## Results (this harness, this environment)

| Metric | Measured |
| --- | --- |
| Quorum write (4 KiB, 3 voters) throughput | 21,865 ops/s |
| p50 latency | 0.021 ms |
| max latency | 2.163 ms |

Raw JSON: `docs/benchmarks/results.json`.

## What this does NOT show

- Nothing about the §11 qualification gates (p99 ≤ 25ms at 1000 ops/s on
  10 GbE + NVMe, `fsync` honor) — different environment, different path
  (in-process, no FUSE, no QUIC, no network).
- Nothing about strict-mode reads (ReadIndex + leases) or group commit.
- Nothing about target-application behavior (M5-02 suite).

No conclusion is drawn from these numbers beyond "the harness runs and the
scale is in the right ballpark for the LAN-synced design".
