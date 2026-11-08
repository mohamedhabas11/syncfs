# ADR 0001 — Defer M5 workload qualification to a later release

**Status:** Accepted (2026-11-08)

## Context

Spec section 11 requires a qualification gate on named reference hardware
(3× NVMe, 10 GbE, p99 ≤ 25 ms at 1000 4 KiB writes/s). Cross-checking
`research/05-protocol-perf.md` (latency-budget arithmetic), the gates are
estimates, not measurements — no run has executed on the named hardware.

## Decision

M5-01/M5-02 are DEFERRED for v0.1.0. v0.1.0 ships the correctness foundation
(M1–M4 + FUSE skeleton) without production claims; the benchmark harness and
target-app suite land alongside M5. The product remains **pre-1.0, not
production ready**.

## Consequences

- Benchmark numbers will come from real runs (`docs/benchmarks/`, recorded
  commit/hardware/kernel/output path); none are claimed in v0.1.0.
- Read/lease and lock surfaces arrive with M2-06/M4 implementations and are
  then measured; ADR-0002-style notes follow if needed.
