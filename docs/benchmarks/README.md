# Benchmarks

Numbers come **only from real runs**. This directory records each run's
methodology so numbers can be reproduced — and judged for what they don't show.

## Policy (laptop / Docker Desktop)

- Every result states its environment: **Docker Desktop, Linux VM**
  (`aarch64, golang:1.26-alpine`), **volume type** (`tmpfs` or `named_volume`;
  never a macOS bind mount for data — `/src` is read-only code), **commit sha**,
  and **exact command** (see each JSON's `command` + `commit_sha` fields).
- **Absolute numbers from this environment are NEVER compared against spec
  targets.** Any spec target requiring specific hardware is marked
  **UNVERIFIED** in `docs/spec/status.md` with the hardware it needs.
- Throughput is reported as a **fraction of the measured fsync floor**
  (`make bench-floor`) plus the **tmpfs-vs-volume gap**. Fresh payloads only;
  dedup state is recorded per table (floor: N/A, raw file; sweep: not in play
  by construction, distinct ChunkIDs verified).
- Netem profiles (0.5 / 2 / 20 ms RTT) live in a **separate, labelled table**
  (`netem.md`), never mixed with the baseline.

Harnesses: `cmd/bench-floor`, `cmd/bench-sweep` (Go, run in-VM via
`make bench-floor` / `make bench-sweep`), plus `test/benchmark/` edge gate
(`test/benchmark/edge.sh`). Render: `python3 scripts/bench-render.py`.

## Compliant results (Docker Desktop, commit `b649526` + uncommitted bench harness)

Fresh-volume runs (`docker volume rm syncfs_bench-data` before, 2026-11-08).
Both `floor-volume.json` / `floor-tmpfs.json` and `sweep-volume.json` /
`sweep-tmpfs.json` record env + volume type + commit + exact command.

| Table | File | What |
|---|---|---|
| floor (write+fsync + RTT) | `floor.md` | 500x 4 KiB `WriteAt`+`Sync`, fresh counter payload, per volume type |
| sweep (quorum writes) | `sweep.md` | 1,8,32,128 clients x 100 ops, 4 KiB fresh quorum writes, 6 fsyncs/entry |
| netem | `netem.md` | separate table — **UNVERIFIED (not run)** |

Clean results (see files for per-run JSON): volume floor ~1,299 ops/s
(p50 0.35 ms), tmpfs floor ~1.15M ops/s (fsync is a no-op in memory);
volume sweep ~867–900 ops/s (0.67–0.69 of floor, p50 1.1 ms → 141 ms as
clients grow 1 → 128), tmpfs sweep ~7.1K–12.9K ops/s; gap tmpfs/volume
~8–14x. **Variance warning:** a warm-cache re-run measured ~8K ops/s on
the same named volume (10x). Treat these as order-of-magnitude VM
observations, not stable targets — re-run after `docker volume rm` for
comparable cold numbers.

## What this does NOT show

- Nothing about the §11 qualification gates (1,000 ops/s, p99 ≤ 25 ms,
  etc.) — different environment (NVMe + 10 GbE required, UNVERIFIED).
- Nothing about QUIC, FUSE, lease revocation, or group commit (sweep is
  in-process fan-out; floor is raw file I/O).
- Nothing about target-application behavior (M5-02).

## Pre-policy runs (retained for provenance only — NOT floor/sweep results)

- Laptop `darwin/arm64`, `t.TempDir()` (macOS filesystem), ~42 ops/s /
  p50 ~24 ms (`results.json` pre-policy part). Dedup-inflated 21,865 ops/s
  duplicate-payload number is superseded by the fresh-payload harness.
- Docker `node-a` run via `test/benchmark/edge.sh` with `t.TempDir()` on
  the container overlay (not tmpfs/named) — also non-compliant under this
  policy. Edge **correctness** gate (`TestEdge`, C1–C8) still stands; its
  **throughput** numbers are not floor/sweep data.
