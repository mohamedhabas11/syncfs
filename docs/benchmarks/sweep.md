# bench-sweep — quorum writes vs client count (fresh 4 KiB)

Docker Desktop Linux VM (aarch64, golang:1.26-alpine). Data on tmpfs or named volume only; never a macOS bind mount. Commit: `d0adf1d` (all runs). Absolute numbers are NEVER compared against spec §11 (UNVERIFIED). Fresh payloads only; dedup not in play by construction (all payloads distinct via counter; verified distinct ChunkIDs == ops).

Outer (repro) commands (Makefile): `docker compose run --rm -e COMMIT_SHA=<sha> bench go run ./cmd/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume` and `... -dir /bench/tmpfs/sweep ... -volume-type tmpfs`. Per-row `Command` is the in-container argv.

Throughput is a fraction of the measured fsync floor (`make bench-floor` on the same volume type). fsyncs/entry = 6 (3 voters x pack Sync + dir Sync). CPU/allocs exclude the fsync wait (wall time).

## Volume type `named_volume` (`/bench/volume/sweep`, floor 2434.4 ops/s)
Command: `/root/.cache/go-build/63/63dafcff7e926c01dc35f2ebe82eb5675ed37a2dac25dbea234f562354ebc4e0-d/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume`

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | 795.2 | 1.14 | 2.55 | 0.327 | 85757 | 52.6 | 0.300 | 0.1 |
| 8 | 896.5 | 8.60 | 15.65 | 0.368 | 85566 | 51.7 | 0.200 | 0.9 |
| 32 | 913.2 | 34.49 | 48.41 | 0.375 | 85574 | 51.7 | 0.212 | 3.5 |
| 128 | 916.6 | 138.54 | 162.97 | 0.377 | 85332 | 51.4 | 0.208 | 14.0 |

## Volume type `tmpfs` (`/bench/tmpfs/sweep`, floor 1045294.7 ops/s)
Command: `/root/.cache/go-build/63/63dafcff7e926c01dc35f2ebe82eb5675ed37a2dac25dbea234f562354ebc4e0-d/bench-sweep -dir /bench/tmpfs/sweep -clients 1,8,32,128 -n 100 -volume-type tmpfs`

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | 9806.4 | 0.04 | 1.23 | 0.009 | 86158 | 54.5 | 0.000 | 0.0 |
| 8 | 12285.8 | 0.05 | 2.98 | 0.012 | 85480 | 51.7 | 0.075 | 0.1 |
| 32 | 11741.0 | 2.65 | 4.70 | 0.011 | 85438 | 51.6 | 0.078 | 0.3 |
| 128 | 13751.1 | 9.04 | 13.55 | 0.013 | 85294 | 51.4 | 0.074 | 0.9 |

## tmpfs-vs-volume gap

| Clients | tmpfs ops/s | volume ops/s | gap (tmpfs/volume) |
|---|---|---|---|
| 1 | 9806.4 | 795.2 | 12.331 |
| 8 | 12285.8 | 896.5 | 13.704 |
| 32 | 11741.0 | 913.2 | 12.857 |
| 128 | 13751.1 | 916.6 | 15.002 |

Higher gap = tmpfs faster (fsync on memory vs disk). Both are Docker VM storage, not reference hardware.
