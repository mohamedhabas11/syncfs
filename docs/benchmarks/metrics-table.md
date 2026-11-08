# Metrics — data-driven development table

Date: 2026-11-08 · Go 1.26.0 darwin/arm64 (Apple M4) · Loop Iterations 1–2 closed.
Workload: `test/benchmark/edge.sh` (`test/benchmark/edge_workload_test.go`).

## A. Final suite (after Iteration 1 fixes)

`go test ./... -count=1 -race` → **20 packages PASS** (3 no-test packages: api/gen, testkit/cluster, testkit/pki). `go vet ./...` → **clean**. Zero `DATA RACE`.

| Area | Before loop | After Iteration 1 |
|---|---|---|
| metadata | 8 attacker FAIL (panics, dot, zombie, overwrite, xattr, truncate) | **PASS** (deterministic IDs, guards, link-count, validName, caps) |
| volume | 4 FAIL (sentinel, ctx, RaftIndex, orphan) | **PASS** (wrap `ErrNoQuorum`, ctx checks, monotonic index, atomic pre-validate) |
| wal | 4 FAIL (gap poison, snapshot lost, Compact, middle silent) | **PASS** (validate-before-write, snapshot restore, Compact>snapshot→error, middle→hard error, dir-sync) |
| chunkstore | 2 FAIL (middle drop, dedup lie) | **PASS** (skip-corrupt-record, verify-on-hit+heal, bounds/nil checks) |
| auth | 2 FAIL (TOCTOU, empty proof) | **PASS** (Put non-empty validation, MarkConsumed refuses re-consume, Consume empty-proof reject) |
| fusefs | 4 FAIL (rename kind, NUL/len, bare xattr, truncate cap) | **PASS** (kind checks, ENAMETOOLONG+NUL, bare-user. reject, MaxFileBytes caps) |
| transport | 3 FAIL (cap, zero, interleave) | **PASS** (WriteFrame control cap 64 KiB + classed variant, zero-length reject, single-Write + mutex) |
| cache | 1 FAIL (leases survive InvalidateAll) | **PASS** (InvalidateAll clears leases) |
| locks | 5/5 already PASS | still PASS |

## B. Edge gate + benchmark (real runs, fresh payloads)

`go test ./test/benchmark/... -run TestEdge -v -count=1 -race` → **PASS** (C1 wal, C2 metadata, C3 volume, C5 auth, C6 fusefs, C7 transport, C8 cache). C4 locks covered by its own 5/5 suite.

| Bench (benchtime=100x) | Measured |
|---|---|
| `BenchmarkWorkloadQuorumWrite` (fresh) | **42.09 ops/s, p50 23.95 ms, p99 32.13 ms, max 43.03 ms** (was 18,063 ops/s p50 0.027 ms dedup artifact) |
| `BenchmarkEdgeFreshQuorumWrite` | 43.86 ops/s, p50 22.89 ms, p99/max 28.97 ms |
| `BenchmarkEdgeMetadataDispatch` | 61.7 µs/op (create+rename, in-memory) |
| `BenchmarkEdgeInvalidNameReject` | 8.5 µs/op (fail-fast) |
| `BenchmarkEdgeAtomicBatchReject` | 1.6 µs/op (pre-validated, no writes) |

Environment: Apple M4, Go 1.26.0, `t.TempDir()`, in-process (no QUIC/FUSE/NVMe). Do **not** compare to §11 (1,000 ops/s, p99 ≤ 25 ms on 10 GbE+NVMe). Honest baseline only.

## C. Maturity gates G1–G10 (critic §F)

| Gate | Status |
|---|---|
| G1 Determinism (two-clone same ID) | **GREEN** (edge C2) |
| G2 WAL crash suite | **GREEN** (edge C1) |
| G3 Chunk recovery | **GREEN** (chunkstore suite + edge) |
| G4 Coordinator (sentinel/ctx/index/receipts/dangling) | **GREEN** (edge C3) |
| G5 Driver gap gating | OPEN (checkChunkGaps stub — known limitation, see below) |
| G6 Auth (atomic + VerifyConnection+role) | PARTIAL: atomic double-spend + empty GREEN (edge C5); per-stream role + VerifyConnection still open |
| G7 FUSE (fail-closed/caps/Lookup/Open) | PARTIAL: WriteFile fail-closed + caps GREEN (edge C6); Lookup inode + Open-flags hardening remain |
| G8 Locks end-to-end (ctx/EDEADLK/wake/wiring) | PARTIAL: family/BindProto/steal GREEN; BlockingGrant(ctx)/EDEADLK/wake/wiring remain |
| G9 Leases/journal/op-IDs | PARTIAL: InvalidateAll leases GREEN (edge C8); Valid Generation + journal durability remain |
| G10 Honesty+harness | PARTIAL: fresh-payload bench GREEN, edge.sh + TestEdge checked in; status demotions + full attacker adoption remain |

Maturity reached: **C1–C8 attacker criteria green with checked-in tests, edge workload script delivered.** Remaining G5/G6/G7/G8/G9 partials are documented as known limitations (do not block edge benchmarks, do block production).

## D. Loop log

- Iter 0: baseline `0fef618`, 30 attacker findings, bench dedup-inflated.
- Critic R2 + Tester R1 consumed (research/01, research/02).
- Iter 1 (this tree): metadata → volume → WAL → chunkstore → auth/cache/transport → FUSE. All shipped suites green after each batch.
- Iter 2: fresh-payload bench fix, `test/benchmark/edge_workload_test.go` + `edge.sh`, rerun, metrics recorded here. Stop condition met: workload script benchmarking edge cases created and passing.
