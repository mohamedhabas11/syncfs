# Getting started

The dev loop is the 3-node container fleet. Every step below is copy-paste
and verified by the CI walkthrough test (`TestVerticalSliceRaftWriteRead` +
`TestVerticalSliceLeaderKillPartitionRestart` + `TestStress*`).

## 1. Build + full suite

```sh
docker compose build
docker compose run --rm node-a go test ./... -count=1 -race
docker compose run --rm node-a go test ./internal/fusefs/ -count=1 -race -v -run TestFUSE
```

`node-a`, `node-b`, `node-c` each own a private writable volume
(`/var/lib/syncfs`) — independence is the point. Shared read-only code is at
`/src`. `/dev/fuse` is routed to every node for M3+ tests.

## 2. Vertical slice (in-process, mirrors the daemons)

The write path is: **payload quorum** (`WriteChunks`, majority of voters) →
attach receipts → **Raft commit** (driver mesh) → apply on every replica →
read on any replica. This is exercised without FUSE first:

```sh
docker compose run --rm node-a go test ./internal/volume/ -run TestVerticalSlice -v -race -count=1
# TestVerticalSliceRaftWriteRead: 3 voters, fresh 4 KiB payload, file "vertical"
#   written via the leader coordinator and read from a follower FSM.
# TestVerticalSliceLeaderKillPartitionRestart: kill leader mid-write, new
#   leader elected, 2/3 majority still writes, replicas catch up.
```

## 3. Chaos (HA acceptance)

```sh
docker compose run --rm node-a go test ./test/benchmark/ -run TestStress -v -race -count=1 -timeout 180s
# Leader/follower loss, partition, interrupted upload, WAL/snapshot/torn/
# middle-corrupt/gap/compact, disk-full contract, corrupt chunk, reconnect,
# learner joint promotion, deadlock (EDEADLK), session expiry.
```

## 4. Bench floor + sweep (Docker Desktop policy)

```sh
make bench-floor   # write+fsync floor + RTT, tmpfs + named volume
make bench-sweep   # 1,8,32,128 clients x 100 ops, 4 KiB fresh
```

Results land in `docs/benchmarks/floor.md` / `sweep.md` with env, volume
type, commit, and exact command. Absolute numbers never compare to §11
(UNVERIFIED — 3× NVMe + 10 GbE).

## 5. What is NOT yet runnable

`syncfs daemon` / `syncfs mount` binaries are validate-and-print stubs
(`docs/reference/cli.md`); the mesh + FUSE handler above are the verification
surface. Production cutover needs the daemon wiring (M5) — see
`docs/known-limitations.md` #6/#17.
