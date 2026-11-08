# bench-sweep — quorum writes vs client count (fresh 4 KiB)

Docker Desktop Linux VM (aarch64, golang:1.26-alpine). Data on tmpfs or named volume only; never a macOS bind mount. Commit: `b649526` (all runs). Absolute numbers are NEVER compared against spec §11 (UNVERIFIED). Fresh payloads only; dedup not in play by construction (all payloads distinct via counter; verified distinct ChunkIDs == ops).

Outer (repro) commands (Makefile): `docker compose run --rm -e COMMIT_SHA=<sha> bench go run ./cmd/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume` and `... -dir /bench/tmpfs/sweep ... -volume-type tmpfs`. Per-row `Command` is the in-container argv.

Throughput is a fraction of the measured fsync floor (`make bench-floor` on the same volume type). fsyncs/entry = 6 (3 voters x pack Sync + dir Sync). CPU/allocs exclude the fsync wait (wall time).

## Volume type `named_volume` (`/bench/volume/sweep`, floor 1298.9 ops/s)
Command: `/root/.cache/go-build/63/63dafcff7e926c01dc35f2ebe82eb5675ed37a2dac25dbea234f562354ebc4e0-d/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume`

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | 869.9 | 1.12 | 1.78 | 0.670 | 85857 | 52.7 | 0.200 | 0.1 |
| 8 | 866.6 | 8.92 | 19.45 | 0.667 | 85581 | 51.7 | 0.212 | 0.9 |
| 32 | 900.2 | 35.31 | 40.89 | 0.693 | 85526 | 51.7 | 0.219 | 3.6 |
| 128 | 896.6 | 140.76 | 173.44 | 0.690 | 85333 | 51.4 | 0.215 | 14.3 |

## Volume type `tmpfs` (`/bench/tmpfs/sweep`, floor 1153179.1 ops/s)
Command: `/root/.cache/go-build/63/63dafcff7e926c01dc35f2ebe82eb5675ed37a2dac25dbea234f562354ebc4e0-d/bench-sweep -dir /bench/tmpfs/sweep -clients 1,8,32,128 -n 100 -volume-type tmpfs`

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | 7090.8 | 0.04 | 2.03 | 0.006 | 86018 | 54.3 | 0.100 | 0.0 |
| 8 | 11547.6 | 0.05 | 3.25 | 0.010 | 85461 | 51.7 | 0.062 | 0.1 |
| 32 | 10968.5 | 2.83 | 5.40 | 0.010 | 85441 | 51.6 | 0.081 | 0.3 |
| 128 | 12863.1 | 9.76 | 18.09 | 0.011 | 85298 | 51.4 | 0.081 | 1.0 |

## tmpfs-vs-volume gap

| Clients | tmpfs ops/s | volume ops/s | gap (tmpfs/volume) |
|---|---|---|---|
| 1 | 7090.8 | 869.9 | 8.151 |
| 8 | 11547.6 | 866.6 | 13.326 |
| 32 | 10968.5 | 900.2 | 12.184 |
| 128 | 12863.1 | 896.6 | 14.346 |

Higher gap = tmpfs faster (fsync on memory vs disk). Both are Docker VM storage, not reference hardware.
