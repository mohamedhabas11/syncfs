# bench-floor — write+fsync floor + RTT

Docker Desktop Linux VM (aarch64, golang:1.26-alpine). Data on tmpfs or named volume only; never a macOS bind mount (`/src` is read-only code).
Commit: `d0adf1d` (both runs). Absolute numbers are NEVER compared against spec §11 targets (UNVERIFIED: needs 3x NVMe honoring flushes + 10 GbE, see docs/spec/status.md). Fresh payloads only; dedup: N/A: raw file write, no chunkstore dedup; counter changes per iteration.
Outer (repro) commands (Makefile): `docker compose run --rm -e COMMIT_SHA=<sha> bench go run ./cmd/bench-floor -dir /bench/volume/floor -volume-type named_volume -n 500 -peer node-b` (named volume) and `... -dir /bench/tmpfs/floor -volume-type tmpfs -n 500 -peer node-b` (tmpfs). Per-row `Command` is the in-container argv.

| Volume | Command | ops/s | p50 ms | p99 ms | max ms | alloc B/op | mallocs/op | CPU ms/op | fsyncs/entry |
|---|---|---|---|---|---|---|---|---|---|
| named_volume (`/bench/volume/floor`) | `/root/.cache/go-build/dd/dde527769976fa4d0dff04977461ca82ca11a2cf804d9bd7f7be61b84b9dba98-d/bench-floor -dir /bench/volume/floor -volume-type named_volume -n 500 -peer node-b` | 2434.4 | 0.17 | 2.95 | 5.87 | 22 | 0.0 | 0.020 | 1 |
| tmpfs (`/bench/tmpfs/floor`) | `/root/.cache/go-build/dd/dde527769976fa4d0dff04977461ca82ca11a2cf804d9bd7f7be61b84b9dba98-d/bench-floor -dir /bench/tmpfs/floor -volume-type tmpfs -n 500 -peer node-b` | 1045294.7 | 0.00 | 0.00 | 0.01 | 22 | 0.0 | 0.000 | 1 |

## RTT (TCP connect, 20 attempts)

| Path | Peer | min ms | p50 ms | p99 ms | Status |
|---|---|---|---|---|---|
| named_volume loopback (127.0.0.1) | self | 0.02 | 0.03 | 0.13 | measured |
| named_volume between-nodes (node-b:22) | node-b | 0.09 | 0.23 | 0.86 | measured TCP connect (DialTimeout 2s, 20 attempts) |
| tmpfs loopback (127.0.0.1) | self | 0.02 | 0.04 | 0.17 | measured |
| tmpfs between-nodes (node-b:22) | node-b | 0.07 | 0.19 | 0.33 | measured TCP connect (DialTimeout 2s, 20 attempts) |

Floor definition: raw `WriteAt` + `f.Sync()` per iteration (1 fsync/entry, dir sync amortized). Sweep throughput below is a fraction of this floor.
