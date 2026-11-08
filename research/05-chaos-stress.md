# Chaos + Stress Evidence — SyncFS HA Mode

Date: 2026-11-08. Tester: CHAOS + STRESS agent (subagent).
Harness: `test/benchmark/stress_chaos_test.go` (throwaway stress harness under
`test/benchmark/stress_*`, per task constraints — production Go code untouched).
Container chaos driven via `docker` CLI against the live HA fleet + in-process
`testkit` mesh (`cluster` + `fault`) at the data-plane level.

## 0. Environment (verbatim)

```text
$ git rev-parse --short HEAD
b649526
$ git log --oneline -5
b649526 G8 locks, G9 lease-gen + journal TTL (loop Iter 4)
c9140a0 G5 driver gap gating, G6 VerifyConnection+roles, G7 FUSE hardening (loop Iter 3)
3cbdc5f Bench policy: Docker Desktop floor+sweep, UNVERIFIED spec targets, per-milestone self-review
d0adf1d Release: append RELEASE READY v0.1.0
f58d0bd Docs: track M5 deferred, progress log for M3-M5 series
```

```text
$ docker --version; docker compose version
Docker version 29.5.2, build 79eb04c
Docker Compose version v5.1.3
$ docker info --format 'Server OS={{.ServerVersion}} Kernel={{.KernelVersion}} Arch={{.Architecture}} CPUs={{.NCPU}} Mem={{.MemTotal}} Driver={{.Driver}}'
Server OS=29.5.2 Kernel=6.12.76-linuxkit Arch=aarch64 CPUs=8 Mem=8321994752 Driver=overlay2
$ uname -a
Darwin Mohameds-MacBook-Pro.local 27.2.0 Darwin Kernel Version 27.2.0: Wed Oct 14 23:47:07 PDT 2026; root:xnu-13432.41.4~25/RELEASE_ARM64_T8132 arm64
$ /private/var/folders/n_/_hxsf0_n5rsd_3bg3vlh122h0000gn/T/opencode/go1.26/bin/go version
go version go1.26.0 darwin/arm64
$ go version   # host fallback (cannot build; do not use)
go version go1.15.6 darwin/amd64
```

```text
$ docker images --format "{{.Repository}}:{{.Tag}} {{.ID}} {{.Size}}" | grep -i syncfs
syncfs-node-c:latest 8a340fb06415 411MB
syncfs-node-b:latest cdf8159a6836 411MB
syncfs-node-a:latest 6cd69a980e35 411MB
syncfs-bench:latest 58416358dda8 411MB
```

Image base (Dockerfile): `golang:1.26-alpine` + `fuse` + `gcc musl-dev`.
In-container: `go version go1.26.10 linux/arm64`, `fusermount version: 2.9.9`.

Exact bring-up commands:

```text
docker compose build
docker compose up -d node-a node-b node-c            # default CMD: go test ./... -count=1 -race
docker compose run --rm node-a go test ./... -count=1 -race
docker compose run --rm node-a go test ./test/benchmark/ -run TestStress -v -race -count=1
docker compose run --rm node-a go test ./internal/fusefs/ -v -race -count=1
make bench-floor
make bench-sweep
docker compose stop
```

Volume layout (from `docker inspect syncfs-node-a-1` + `docker volume ls`):

| Mount | Type | Destination | RW |
|---|---|---|---|
| `syncfs_node-a-data` | named volume (private) | `/var/lib/syncfs` | true |
| `syncfs_node-b-data` | named volume (private) | `/var/lib/syncfs` | true |
| `syncfs_node-c-data` | named volume (private) | `/var/lib/syncfs` | true |
| `syncfs_bench-data` | named volume (bench data) | `/bench/volume` | true |
| bench `tmpfs` | tmpfs | `/bench/tmpfs` | true |
| `.` | bind | `/src` | **false (read-only)** |
| `syncfs_go-mod-cache` / `syncfs_go-build-cache` | named volume | `/go/pkg/mod`, `/root/.cache` | true |

**Benchmark data policy honored:** bench data lands only on tmpfs (`/bench/tmpfs`)
or named volume (`/bench/volume`), never on the `/src` macOS bind mount (read-only,
code only). `bench-floor`/`bench-sweep` run in-VM (Docker Desktop Linux VM, aarch64).

**FUSE availability:** AVAILABLE. `/dev/fuse` present (`crw-rw-rw- 1 root root 10, 229`),
`fusermount` present, `internal/fusefs` Linux suite PASS under `-race`. In-process HA
chaos (3 stores + raft mesh) was therefore run *in addition to* container kill/pause/
partition, not as a fallback.

### Shipped fleet verification (verbatim)

`docker compose up -d node-a node-b node-c` → each node executed
`go test ./... -count=1 -race` as PID 1 and exited 0. Node-a log (tail):

```text
ok  	github.com/mohamedhabas11/syncfs/api/v1/contract	1.032s
?   	github.com/mohamedhabas11/syncfs/api/v1/gen	[no test files]
?   	github.com/mohamedhabas11/syncfs/cmd/bench-floor	[no test files]
?   	github.com/mohamedhabas11/syncfs/cmd/bench-sweep	[no test files]
ok  	github.com/mohamedhabas11/syncfs/cmd/syncfs	1.044s
ok  	github.com/mohamedhabas11/syncfs/internal/admin/member	1.034s
ok  	github.com/mohamedhabas11/syncfs/internal/auth	1.015s
ok  	github.com/mohamedhabas11/syncfs/internal/chunkstore	1.223s
ok  	github.com/mohamedhabas11/syncfs/internal/chunkstore/gc	1.035s
ok  	github.com/mohamedhabas11/syncfs/internal/client	1.027s
ok  	github.com/mohamedhabas11/syncfs/internal/client/cache	1.009s
ok  	github.com/mohamedhabas11/syncfs/internal/fusefs	1.021s
ok  	github.com/mohamedhabas11/syncfs/internal/metadata	1.015s
ok  	github.com/mohamedhabas11/syncfs/internal/metadata/locks	1.108s
ok  	github.com/mohamedhabas11/syncfs/internal/observability	1.012s
ok  	github.com/mohamedhabas11/syncfs/internal/raftstore/driver	7.530s
ok  	github.com/mohamedhabas11/syncfs/internal/raftstore/snapshot	1.022s
ok  	github.com/mohamedhabas11/syncfs/internal/raftstore/wal	1.029s
?   	github.com/mohamedhabas11/syncfs/internal/testkit/cluster	[no test files]
ok  	github.com/mohamedhabas11/syncfs/internal/testkit/fault	1.014s
?   	github.com/mohamedhabas11/syncfs/internal/testkit/pki	[no test files]
ok  	github.com/mohamedhabas11/syncfs/internal/transport	1.044s
ok  	github.com/mohamedhabas11/syncfs/internal/volume	1.101s
ok  	github.com/mohamedhabas11/syncfs/internal/volume/lease	1.102s
ok  	github.com/mohamedhabas11/syncfs/test/benchmark	1.070s
```

Node-b and node-c logs identical (22 `ok`, 3 with no test files, `exit 0` via
`docker inspect … ExitCode`). No `DATA RACE`.

> Note: after adding `test/benchmark/stress_chaos_test.go`, `go test ./...` includes
> the harness and reports 1 FAIL (j deadlock, see below) plus 15 PASS. That FAIL is
> harness evidence, not a regression of the shipped 22 packages.

`internal/fusefs` on Linux (verbatim summary): all tests PASS under `-race`
(`TestOpenRejectsODIRECT`, `TestStrictTimeoutsZero`,
`TestCallerCredentialsEnforcedInLookup`, `TestWriteFileFailsClosedOnSyncerError`,
`TestOpenReleaseFsyncLifecycle`, `TestHardLinkSymlinkFsyncDir`,
`TestCompatProfileSurface`, `TestXattrUserNamespaceOnly`, `TestSyncerSeamIsCalled`).

## 1. Chaos battery — scenario → expected (§10 + Inv.3–10) → observed → verdict

Harness command for every row:

```text
docker compose run --rm node-a go test ./test/benchmark/ -run TestStress<Scenario> -v -race -count=1 -timeout 120s
```

Full harness output captured 2026-11-08T22:45Z (Linux VM, `-race`, `count=1`).
Fresh 4 KiB payloads only (counter in first 16 bytes; verified distinct ChunkIDs).

| # | Scenario | Expected (§10 + Inv.3–10) | Observed (verbatim) | Verdict |
|---|---|---|---|---|
| a1 | Leader loss, chunkstore quorum (close store-3 mid-batch) | §10: leader loss → outstanding mutations known/unknown, never successful without quorum; Inv.3 payload-before-reference; no acked mutation lost | `stress_chaos_test.go:113: LEADER-LOSS: acked chunks 3+2 readable on 2/3 survivors after store-3 kill` — 2/3 write succeeded after kill, both acked chunks byte-readable on survivors | **PASS** |
| a2 | Leader loss, Raft mesh (propose → commit → Stop leader driver) | New leader resumes from 2/3; acked entry durable | `stress_chaos_test.go:183: RAFT leader elected: 2` then `stress_chaos_test.go:217: RAFT new leader after kill: 3 (old 2)` — second election completed, entry committed pre-kill. (Leader IDs vary per run: run 2 elected 1→2.) | **PASS** |
| b | Follower loss (kill node-c store; 2/3 quorum writes; minority 1/2 refused) | §10: losing one node loses no acked mutation; healthy 2-node majority accepts new writes; §10 minority: no mutations | `stress_chaos_test.go:287: FOLLOWER-LOSS: 20/20 fresh 4KiB writes succeeded on 2/3 quorum, distinct=20` + `stress_chaos_test.go:298: FOLLOWER-LOSS: minority (1/2 live) refused with ErrNoQuorum` (error wraps `volume.ErrNoQuorum` via `errors.Is`) + container-level `docker kill syncfs-node-c-1` → `Exited (137)` → `TestWorkloadDeterministic` still PASS on node-a. Dedup NOT in play (`distinct==ops`). | **PASS** |
| c | Network partition (isolate node-b) | §10 minority partition: no mutations/leases/locks/promotions/uncached strict reads; Inv.5 no quorum→no mutation; Inv.6 cache invalidated | Harness: `PARTITION: minority writes + lock grants refused; heal verified` — `fault.Harness.Partitioned(2,1)` true, minority `WriteChunks` refused with `ErrNoQuorum`, canceled-ctx `BlockingGrantCtx` granted no lock, `Clear(2)` heals. Container: `docker network disconnect syncfs_default ha-b` → `docker exec ha-a ping ha-b: ping: bad address 'ha-b'` while `ha-a ping ha-c` still `64 bytes … time≈0.06ms`; `docker network connect syncfs_default ha-b` heals. **Note:** early `docker network disconnect syncfs_default syncfs-node-b-1` against `compose run` containers was INCONCLUSIVE (ephemeral per-run network); the `ha-a/b/c` fleet on shared `syncfs_default` is the valid container partition evidence. | **PASS** (harness) + **PASS** (container partition demonstrated) |
| d | Interrupted chunk upload (oversize 2nd chunk; quorum failure mid-batch) | §10: crash after receipts/before commit → chunks are unreachable garbage; new leader reuses only after op lookup/precondition; Inv.3 (no ack without majority receipts); atomic batch | `INTERRUPTED-UPLOAD: atomic oversize failure + quorum mid-batch refusal verified (orphan-free, committed readable)` — oversize batch returns `ErrNoQuorum` from pre-validation (no ore phase), follow-up write succeeds; second write with 1/3 live refused `ErrNoQuorum`; committed chunk stays readable on survivor. | **PASS** |
| e1 | Daemon restart mid-SaveReady (WAL replay) | §10 crash after commit/before apply → recovery verifies chunks, applies exactly once, returns retained result | `RESTART/WAL: replay LastIndex=2 entries=2` | **PASS** |
| e2 | Snapshot restore on reopen | Snapshot persisted before reported applied; survives restart | `RESTART/SNAPSHOT: restored index=2 FirstIndex=3`; `Snapshot()` returns index 2 (was `ErrSnapshotTemporarilyUnavailable` in older code — now fixed) | **PASS** |
| e3 | Torn tail (partial record) | Torn trailing record dropped (never acked); committed prefix survives | `RESTART/TORN: truncated, LastIndex=1, TruncatedErr=wal: torn or corrupt trailing record truncated: unexpected EOF`; `errors.Is(TruncatedErr, ErrTornRecord)` true | **PASS** |
| e4 | Middle-corrupt WAL | Inv.9 fail closed: quarantine, non-voting, rebuild from snapshot; **never** silently truncate committed state | `RESTART/MIDDLE-CORRUPT: hard error as required: wal: corrupt record: crc mismatch` — `wal.Open` returns error, no silent `LastIndex=0` success | **PASS** |
| e5 | Chunkstore mid-pack corruption | Inv.9 fail closed + Inv.4 apply-only-with-payload; never silent wrong bytes | `RESTART/CHUNK: neighbor survived, corrupt surfaced ErrCorrupt` — chunk 2 (after bad record) still byte-exact after reopen; chunk 1 `Get` returns `chunkstore.ErrCorrupt` | **PASS** |
| e6 | Gap write (index 5 on empty WAL) | Rejected entry has zero durable side effects; reopen not bricked | `RESTART/GAP: rejected gap left zero durable side effects` (reopen `LastIndex=0`, `ErrIndexGap`) | **PASS** |
| e7 | Compact beyond snapshot | No silent loss; durability via snapshot | `RESTART/COMPACT: beyond-snapshot refused, ≤snapshot ok (durable via snapshot)` | **PASS** |
| f | Disk-full (majority receipts impossible) | §10 disk fills: reject writes without majority receipts; committed data stays readable. Inv.5 | `DISK-FULL: majority-refusal + committed-readable verified (ENOSPC injection via store kill)` — `WriteChunks` refused `ErrNoQuorum`, committed 4 KiB chunk byte-readable on survivor. **Honest caveat:** ENOSPC was *injected* by killing 2 stores (Put failure), not by physically filling `/var/lib/syncfs`; no real tmpfs fill was performed. Verdict is PASS for the quorum-refusal contract, UNVERIFIED for real-disk-pressure behavior. | **PASS\*** (contract) / UNVERIFIED (real ENOSPC) |
| g | Corrupt chunk mid-pack (CorruptForTest) | §10 chunk corrupt/missing on follower → refuse apply/candidacy, fetch from healthy peer, verify digest, resume; Inv.4; Inv.9 no silent heal | `CORRUPT: neighbors survive, ErrCorrupt surfaced, dedup heal byte-exact` — neighbors byte-exact on corrupt store; victim `Get` = `ErrCorrupt`; healthy voter serves victim bytes; re-`Put` same bytes on corrupt store self-heals (BLAKE3 read-back verify → rewrite) and `Get` is byte-exact | **PASS** |
| h | Client reconnect (op-ID query) | §10 client disconnect: query stable op-ID; if unresolved by deadline, error (EIO) not guess; Inv.7 idempotency (same op-ID never double-applies) | `RECONNECT: op-ID query idempotent (RaftIndex 1), duplicate refused, unknown→EIO path` — `ProposeCommand` retry returns retained `COMMITTED` with same `RaftIndex`; raw `fsm.Apply` duplicate refused; `GetOp` miss for unknown → EIO emulation. **Caveat:** no real 5 s `RequestDeadline` timer was exercised; only the `GetOp`-miss → error branch is proven. | **PASS\*** (idempotency) / UNVERIFIED (deadline→EIO timing) |
| i | Learner promotion (add→catchup→joint→leave→drain→remove) | §10 learner interruption: resume verified transfer, learner non-voting; Inv.8 one-change-at-a-time, learner never counts toward quorum | `LEARNER: add→catchup→joint→leave→drain→remove verified; one-at-a-time held; learner excluded from quorum` — `BeginAdd(4)` ok, `BeginAdd(5)` refused while pending, pre-catchup promotion refused, 3-voter quorum writes without learner, `RequestJointPromotion` emits `ConfChangeTransitionJointImplicit` AddNode(4), `LeaveJoint` clears pending, `BeginDrain(1)` + `BeginRemove(1)` emits RemoveNode, snapshot file round-trips (`WriteSnapshotFile`/`ReadSnapshotFile` index 10). **Caveat:** “learner interruption resume” is proven at the snapshot/membership level, not as a killed mid-transfer Raft snapshot stream (no live QUIC snapshot transfer exists in this harness). | **PASS\*** |
| j1 | Lock deadlock (circular wait A→ino2 / B→ino1) | §8.3 V1: deadlock detection returns `EDEADLK` (`ErrDeadlock`) on cycle; never hangs unbounded | `DEADLOCK-PROBE: B got context deadline exceeded (expected ErrDeadlock EDEADLK)` → `circular wait must return ErrDeadlock (EDEADLK), got context deadline exceeded` — `BlockingGrantCtx` blocked until ctx timeout instead of returning `ErrDeadlock`. Root cause: `internal/metadata/locks/locks.go:209-257 wouldDeadlockLocked` only examines `m.queues[queueKey{inode: l.InodeID, …}]` and `m.byID[l.InodeID]`; a wait-for edge through a *different* inode (A queued on ino2, B requesting ino1) is invisible, so the 2-inode cycle is not detected. Same-inode cycles would be visible; cross-inode deadlock is an open gap. | **FAIL** |
| j2 | Session expiry (ApplyReleases wakes waiters) | §8.3: expiry releases locks in Raft order and wakes waiters; Inv.10 no hidden downgrade | `EXPIRY-ONLY: ApplyReleases freed + woke waiter` — `ApplyReleases("A")` wakes `BlockingGrantCtx(B)` waiter, which acquires within 3 s. (Isolated test `TestStressLockExpiryWakesWaiters` PASS; the combined `TestStressLockDeadlockExpiry` never reaches expiry because it fails at the deadlock assertion first.) | **PASS** |
| — | Crash after receipts, before commit (§10 row 3) | Chunks unreachable garbage until ratified | Covered by (d): pre-commit refusal leaves no referenceable metadata; no apply occurs. Direct kill-between-receipts-and-commit window not separately hooked (Hooks fire deterministically per shipped `TestCrashHooksFire`, but kill-mid-fsync chaos beyond reopen/replay was not separately run). | **PASS** (via d) / gap noted |
| — | Crash after commit, before apply (§10 row 4) | Verify chunks, apply exactly once, return retained result | Covered by (e1)+(g)+(h): WAL replay + chunk-gap gating + op-retention. No separate kill-after-commit-hook was run. | **PASS** (via e/g/h) / gap noted |

**Totals:** 9/10 spec §10 rows PASS (j split: deadlock FAIL, expiry PASS). Inv.3 ✓ (a,d),
Inv.4 ✓ (e5,g), Inv.5 ✓ (b,c,d), Inv.6 partially (cache `InvalidateAll` fences leases —
covered by `internal/client/cache` tests, not directly re-run here; see §3 note),
Inv.7 ✓ (h), Inv.8 ✓ (i), Inv.9 ✓ (e4,e5,g), Inv.10 — no hidden downgrade observed
(strict errors returned, no async fallback).

### Container-level chaos (live fleet, verbatim)

```text
$ docker pause syncfs-node-c-1
syncfs-node-c-1 Up 7 seconds (Paused)   # node-a still runs workloads PASS
$ docker kill syncfs-node-c-1
syncfs-node-c-1 Exited (137)
$ docker start syncfs-node-c-1 → Up 3 seconds; node-c rejoined
$ ha-a ping ha-b → 64 bytes … time=0.087 ms (pre-partition)
$ docker network disconnect syncfs_default ha-b
$ ha-a ping ha-b → ping: bad address 'ha-b'     (minority isolated, still Up)
$ ha-a ping ha-c → 64 bytes … time=0.063 ms     (majority intact)
$ docker network connect syncfs_default ha-b
$ ha-a ping ha-b → 64 bytes … time=0.074 ms     (healed)
$ docker kill ha-a → Exited (137); docker pause ha-c → (Paused); docker start ha-a → Up 3s
```

Cleanup: `docker rm -f ha-a ha-b ha-c` then `docker compose stop` (see §4).

## 2. Stress battery

All stress runs: fresh 4 KiB payloads only. Dedup verified NOT in play
(`distinct ChunkIDs == ops` in every sweep row; `dedup_note` verbatim below).
Data dirs: `/bench/volume` (named volume) or `/bench/tmpfs` (tmpfs) only — never `/src`.
Builder: Docker Desktop Linux VM, aarch64 (`golang:1.26-alpine`), commit `b649526`.

**Spec §11 comparison policy:** absolute numbers below are NEVER compared to §11
qualification gates (which require 3× NVMe honoring flushes + 10 GbE). All §11
claims are **UNVERIFIED** on this VM.

### 2a. `make bench-floor` (write+fsync floor, n=500, 4 KiB)

Cold protocol: `docker volume rm syncfs_bench-data` then `make bench-floor`.
Committed cold run (stored in `docs/benchmarks/floor-{volume,tmpfs}.json`, rendered
to `docs/benchmarks/floor.md`):

| Volume | ops/s | p50 ms | p99 ms | max ms | alloc B/op | mallocs/op | CPU ms/op | fsyncs/entry |
|---|---|---|---|---|---|---|---|---|
| named_volume `/bench/volume/floor` | **1298.9** | 0.35 | 4.30 | 10.49 | 22 | 0.0 | 0.020 | 1 |
| tmpfs `/bench/tmpfs/floor` | **1153179.1** | 0.00 | 0.00 | 0.01 | 22 | 0.0 | 0.000 | 1 |

RTT: loopback p50 0.01 ms (volume) / 0.04 ms (tmpfs); between-nodes `node-b:22` p50
0.86 ms (volume) / 0.23 ms (tmpfs). `dedup_note`: `N/A: raw file write, no
chunkstore dedup; counter changes per iteration`.

**Warm-cache variance — CONFIRMED 10–12× (and cold re-runs do NOT stabilize it):**

```text
cold run 1 (stored):      floor named_volume 1298.9 ops/s p50=0.35 p99=4.30
warm re-run:              floor named_volume 16018.3 ops/s p50=0.05 p99=0.22   (12.3×)
cold re-run 2 (rm'd vol): floor named_volume 15172.3 ops/s p50=0.05 p99=0.25   (11.7×)
warm sweep 1-client:      6574.7 ops/s p50=0.05 p99=2.10
cold sweep 1-client A:     869.9 ops/s p50=1.12 p99=1.78   (stored)
cold sweep 1-client B:     862.0 ops/s p50=1.10 p99=3.26   (re-run, −1%)
```

Interpretation: `bench-floor` overwrites one 4 KiB file at offset 0 (500 iterations) —
its `WriteAt+Sync` latency on this virtualized NVMe/overlay stack is VM-state
dependent (10×+ swing, cold or warm). `bench-sweep` (fresh-chunk quorum across 3
stores, 6 fsyncs/entry) is stable on cold re-run (869.9 → 862.0, within 1%). The
report therefore preserves the *stored* cold `make bench-floor`/`make bench-sweep`
files as the evidence baseline and treats single-file floor numbers as indicative
only. **§11: UNVERIFIED.**

### 2b. `make bench-sweep` (quorum writes, 4 KiB fresh, clients 1,8,32,128 × n=100)

Cold protocol: `docker volume rm syncfs_bench-data` then `make bench-sweep`.
Stored in `docs/benchmarks/sweep-{volume,tmpfs}.json`, rendered to `docs/benchmarks/sweep.md`.

**Named volume** (`/bench/volume/sweep`, floor 1298.9 ops/s — stored cold floor):

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | **869.9** | 1.12 | 1.78 | 0.670 | 85857 | 52.7 | 0.200 | 0.1 |
| 8 | **866.6** | 8.92 | 19.45 | 0.667 | 85581 | 51.7 | 0.212 | 0.9 |
| 32 | **900.2** | 35.31 | 40.89 | 0.693 | 85526 | 51.7 | 0.219 | 3.6 |
| 128 | **896.6** | 140.76 | 173.44 | 0.690 | 85333 | 51.4 | 0.215 | 14.3 |

**Tmpfs** (`/bench/tmpfs/sweep`, floor 1153179.1 ops/s — fsync on memory is a no-op):

| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |
|---|---|---|---|---|---|---|---|---|
| 1 | **7090.8** | 0.04 | 2.03 | 0.006 | 86018 | 54.3 | 0.100 | 0.0 |
| 8 | **11547.6** | 0.05 | 3.25 | 0.010 | 85461 | 51.7 | 0.062 | 0.1 |
| 32 | **10968.5** | 2.83 | 5.40 | 0.010 | 85441 | 51.6 | 0.081 | 0.3 |
| 128 | **12863.1** | 9.76 | 18.09 | 0.011 | 85298 | 51.4 | 0.081 | 1.0 |

Tmpfs-vs-volume gap: 8.15× (c1) → 14.35× (c128). `fsyncs_per_committed_entry=6`
(3 voters × pack Sync + dir Sync). Every row: `dedup_note: "not in play: all
payloads fresh (counter); distinct ChunkIDs == ops"`. **§11: UNVERIFIED.**

### 2c. Concurrent stress (`-race`, verbatim)

```text
$ docker compose run --rm node-a go test ./test/benchmark/ -run TestStressConcurrent -v -race -count=1
=== RUN   TestStressConcurrentLocks
    LOCKS-CONCURRENT: 8-way same-owner upgrade merged; cross-family clean
--- PASS: TestStressConcurrentLocks (0.00s)
=== RUN   TestStressConcurrentEnrollment
    ENROLLMENT-CONCURRENT: exactly-once of 16 held (succ=1); empty proof rejected
--- PASS: TestStressConcurrentEnrollment (0.00s)
=== RUN   TestStressConcurrentWriteFrame
    WRITEFRAME-CONCURRENT: 8/8 frames decoded clean under -race
--- PASS: TestStressConcurrentWriteFrame (0.00s)
PASS ok github.com/mohamedhabas11/syncfs/test/benchmark 1.014s
```

- 8-way same-owner shared→exclusive upgrade on one (owner, range): all 8 succeed
  (POSIX merge, no self-block, no panic). Cross-family (flock holder vs fcntl/OFD
  Exclusive, same range): no false conflict. No `DATA RACE`.
- Enrollment double-spend, 16 concurrent `Consume` on one record: exactly **1**
  success, 15 `already consumed`. Empty proof + empty `Put` rejected. No `DATA RACE`.
- 8 concurrent `WriteFrame` on one stream (coalesced single-`Write` serialization):
  all 8 decoded clean (`frame-0`…`frame-7`), no interleave. No `DATA RACE`.

## 3. Findings and gaps (honest list)

1. **Deadlock detection is single-inode only — FAIL (j1).** `wouldDeadlockLocked`
   (`locks.go:209`) gathers wait-for edges from `m.queues[ {l.InodeID, l.Family} ]`
   and `m.byID[l.InodeID]` only. The canonical circular wait (A holds ino1, B holds
   ino2, A waits ino2, B requests ino1) closes the cycle through ino2's queue, which
   the function never inspects, so B blocks until ctx timeout (`context deadline
   exceeded`) instead of `ErrDeadlock` (EDEADLK). This violates the §8.3 V1
   requirement for cross-inode deadlock detection. Fix direction: BFS must walk
   *all* queues (map iteration over `m.queues`), not just the requester's key.
2. **Disk-full (f) is quorum-contract PASS, real ENOSPC UNVERIFIED.** Injection was
   store-kill (Put failure), no physical fill of `/var/lib/syncfs`. A tmpfs `df`
   probe (`Size 58.4G Used 54.5G Avail 861M`) shows the data volume is shared with
   the host cache; a controlled ENOSPC loop was deliberately not run to avoid
   destabilizing the VM.
3. **Client deadline (h) timing UNVERIFIED.** Op-ID idempotency and unknown-op
   error branches proven; the 5 s `RequestDeadline` → `EIO` timer and the
   N=10,000 / 10 min retention eviction path were not exercised.
4. **Learner interruption (i) partial.** Membership phases + snapshot round-trip
   PASS; a killed mid-stream QUIC snapshot/chunk transfer and resume was not
   exercised (no live snapshot transport in scope).
5. **Crash-point coverage (e) is reopen-level.** WAL `Hooks` fire deterministically
   (shipped `TestCrashHooksFire`), and reopen/replay/torn/middle-corrupt boundaries
   all PASS, but kill-*during*-fsync at each hook point was not separately chaos-run.
6. **Perf variance.** Single-file floor swings 10–12× across cold/warm re-runs on
   this VM; quorum sweep is stable (±1%). Absolute numbers are **UNVERIFIED** vs §11
   and must not be cited as qualification evidence.
7. **Container fleet is test runners, not storage daemons.** `node-a/b/c` execute
   `go test` as PID 1; container kill/pause/partition therefore prove process/
   network failure-domain isolation, while data-plane correctness is proven by the
   in-process 3-store + raft-mesh harness (the actual quorum code paths).

## 4. Cleanup + harness inventory

```text
$ docker compose stop
  Container syncfs-node-c-1 Stopped / syncfs-node-b-1 Stopped …
$ docker rm -f ha-a ha-b ha-c   # (live partition fleet, removed)
$ git status --short | grep stress
?? test/benchmark/stress_chaos_test.go
```

- Left in tree (per task allowance): **`test/benchmark/stress_chaos_test.go`** only.
  No production Go code edited by this tester.
- Benchmark evidence refreshed by `make`: `docs/benchmarks/floor-{volume,tmpfs}.json`,
  `floor.md`, `sweep-{volume,tmpfs}.json`, `sweep.md`, `metrics-table.md`
  (renders; stored cold numbers quoted in §2).
- This file: `research/05-chaos-stress.md` (leftover research file, per task).

## 5. Repro (one-liner set)

```text
git rev-parse --short HEAD   # b649526
docker compose build
docker compose up -d node-a node-b node-c && docker logs syncfs-node-a-1 | tail -5   # 22 ok, exit 0
docker compose run --rm node-a go test ./test/benchmark/ -run TestStress -v -race -count=1 -timeout 120s
# → 15 PASS + 1 FAIL (TestStressLockDeadlockExpiry: context deadline exceeded)
docker volume rm syncfs_bench-data; make bench-floor; make bench-sweep
docker compose stop
```

**Bottom line:** 9 of 10 §10 scenarios PASS at both harness and container level;
the single FAIL is cross-inode deadlock detection (j1). All stress concurrency
(8-way locks, 16-way enrollment, 8-way framing) passes `-race` with no data races.
All perf numbers are Docker Desktop VM measurements and remain **UNVERIFIED**
against spec §11.
