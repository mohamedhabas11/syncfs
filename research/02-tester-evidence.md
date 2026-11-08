# Tester Evidence — SyncFS Adversarial Testing

Date: 2026-11-08. Environment: Go 1.26.0 (`GOTOOLCHAIN=local`,
host `darwin/arm64` via
`/private/var/folders/n_/_hxsf0_n5rsd_3bg3vlh122h0000gn/T/opencode/go1.26/bin/go`)
and Linux `node-a` via `docker compose run --rm node-a` (Docker run confirmed
the same failures on Linux; FUSE smoke tests executed on Linux).
All existing project tests in the 8 target packages PASS before/after the
attack runs (each package reports FAIL only because of the injected attacker
tests). `-race` stress runs produced no `DATA RACE` warnings — the findings
below are logic/contract violations, not memory races (the enrollment
TOCTOU is a check-then-act race *across* mutexes, invisible to the race
detector).

Per task constraints no production code was edited. Attack tests were
exercised as ephemeral `_attacker` test files in each target package; their
names and red output are preserved verbatim below for replay.

## 1. Verdicts per target

| Target | Verdict | Confirmed findings |
|---|---|---|
| `internal/chunkstore` | **Attack succeeded** | 2 (1 durability loss, 1 false-durable receipt) / 2 survived |
| `internal/raftstore/wal` | **Attack succeeded** | 4 (bricked log, lost snapshot, undurable compact, silent truncation) |
| `internal/metadata` | **Attack succeeded** | 5 (2 nil-map panics, invalid names, zombie inode) |
| `internal/volume` | **Attack succeeded** | 4 (sentinel error, ignored ctx, constant index, orphan) / 1 survived |
| `internal/metadata/locks` | **Attack succeeded** | 5 (segfault, starvation, family confusion, proto binding, self-block) |
| `internal/auth` | **Attack succeeded** | 2 (TOCTOU double-spend, empty-proof) / 2 survived |
| `internal/fusefs` | **Attack succeeded** | 5 (rename clobber, unvalidated names, bare xattr, overflow panic, mtime no-op) |
| `internal/transport` | **Attack partially succeeded** | 3 low-severity framing findings; TLS negatives mostly survived |

Total: 30 confirmed findings (counting each `_` named test below once).

## 2. Failing tests (attack succeeded — verbatim name, red output, root cause)

### 2.1 `internal/chunkstore` (host + container identical)

- `TestAttackerMiddleCorruptionDropsLaterDurableChunk`
  ```text
  attacker_evidence_test.go:40: later durable chunk destroyed by earlier-record corruption: Get err=chunkstore: data corrupt (Has=true)
  ```
  Root cause: `Open`/`scanPack` (chunkstore.go:127-151) treats the first
  CRC/length failure as a torn *tail* and truncates the pack at that
  offset — even when the bad record is mid-file. A committed, indexed,
  receipt-issued chunk 2 that lives after the corrupted chunk 1 record is
  physically amputated, while Pebble still reports `Has= true` and `Get`
  fails. Durability contract ("Put fsyncs before indexing") is violated:
  an fsynced, indexed chunk is silently destroyed by damage to an
  *earlier* record.
- `TestAttackerPutAfterCorruptionClaimsSuccessWithoutHealing`
  ```text
  attacker_evidence_test.go:71: Put returned durable receipt for corrupt chunk but Get fails: chunkstore: data corrupt
  ```
  Root cause: `Put` (chunkstore.go:172-176) returns success purely from
  the Pebble index without re-verifying on-disk bytes. After
  `CorruptForTest`, re-`Put` of identical bytes returns a fresh "durable"
  receipt for bytes that are not durable. Idempotent dedup needs a
  verification-on-hit branch.

### 2.2 `internal/raftstore/wal`

- `TestAttackerGapWritePoisonsDurableLog`
  ```text
  attacker_evidence_test.go:28: reopen bricked after rejected gap write: wal: entry index gap (got 5 want 1)
  ```
  Root cause: `SaveReady` (wal.go:207-217) appends to the file *before*
  the index-contiguity check, and `appendEntryLocked` (wal.go:152-162)
  appends to `s.ents` before validating `e.Index`. A rejected gap entry
  is therefore persisted and also mutates in-memory state. On reopen,
  `replay` loads that record and `Open` fails with `ErrIndexGap` —
  a single rejected `SaveReady` permanently bricks the WAL.

- `TestAttackerSnapshotNotRestoredOnReopen`
  ```text
  attacker_evidence_test.go:72: snapshot lost on reopen: snapshot is temporarily unavailable
  ```
  Root cause: `SaveReady` (wal.go:229-236) writes snapshots via
  `snapshot.WriteSnapshotFile`, but `Open`/`replay` (wal.go:91-131) never
  calls `snapshot.ReadSnapshotFile`. After `Close`/`Open`, `Snapshot()`
  returns `ErrSnapshotTemporarilyUnavailable` and `FirstIndex` reverts.
  Snapshot state is write-only across restarts.

- `TestAttackerCompactUndoneByReopen`
  ```text
  attacker_evidence_test.go:110: compacted entries resurrected after reopen: Entries(1) err=<nil>
  ```
  Root cause: `Compact` (wal.go:348-363) discards entries only in memory
  and never rewrites/truncates `wal.log`. `replay` re-reads the file, so
  compacted entries return after restart and `Entries` no longer yields
  `ErrCompacted`. (Relatedly the wal.log is never GC'd — unbounded growth.)

- `TestAttackerMiddleCorruptionSilentlyTruncatesCommittedEntries`
  ```text
  attacker_evidence_test.go:152: silent truncation: reopen succeeded with LastIndex=0 (want 3 or a hard error)
  ```
  Root cause: replay (wal.go:110-129) `break`s on the first
  `decodeRecord` failure and truncates the file at that offset, reporting
  success — regardless of whether the failure is at the tail (torn write,
  correct to drop) or mid-log (bit-rot in a committed entry). Committed
  entries 2-3 are silently lost with `LastIndex=0`.

### 2.3 `internal/metadata`

- `TestAttackerLinkToMissingParentPanics`
  ```text
  attacker_evidence_test.go:39: LINK to missing parent panicked: assignment to entry in nil map
  ```
  Root cause: `doLink` (metadata.go:312-338) never checks that the
  *parent* exists/is a dir. `m.dentries[parent]` is nil for unknown
  parents, so the dentry write (line 335) panics. A single crafted
  `LINK` command crashes the state machine (and therefore the Raft
  applier). Same defect class in `doRename`:
- `TestAttackerRenameToMissingDstParentPanics`
  ```text
  attacker_evidence_test.go:48: RENAME to missing dst parent panicked: assignment to entry in nil map
  ```
  Root cause: `doRename` (metadata.go:358-378) validates only the source
  dentry; `m.dentries[dstPar]` (line 374) is nil for a missing dst
  parent → panic.

- `TestAttackerDotNamesAccepted`
  ```text
  attacker_evidence_test.go:78: CREATE name "." must be rejected
  attacker_evidence_test.go:78: CREATE name ".." must be rejected
  ```
  Root cause: `validName` (metadata.go:404-409) rejects only empty,
  `>255`, `/`, NUL. `"."` and `".."` are accepted as real dentries.
- `TestAttackerRenameAcceptsSlashInDstName`
  ```text
  attacker_evidence_test.go:95: RENAME to slash name must be rejected
  ```
  Root cause: `doRename` performs no `validName` check on
  `SrcName`/`DstName` at all, so the separator validation that `CREATE`
  gets can be bypassed via `RENAME` to materialize `/`-containing names.
- `TestAttackerUnlinkLeavesZombieInode`
  ```text
  attacker_evidence_test.go:120: write/truncate of unlinked (last-link) inode must be NotFound
  ```
  Root cause: `doUnlink` (metadata.go:340-356) deletes only the dentry.
  There is no link count and no inode free path, so after the final
  unlink the inode (manifest, size, rev) remains fully addressable:
  `TRUNCATE`/write commands on a "deleted" inode still succeed.

### 2.4 `internal/volume`

- `TestAttackerNoQuorumErrorIsSentinel`
  ```text
  attacker_evidence_test.go:26: error must wrap ErrNoQuorum for errors.Is, got: volume: chunk 0 durable on 1/2 voters (need 2); refusing before commit
  ```
  Root cause: `WriteChunks` (volume.go:52) returns a bare `fmt.Errorf`,
  never wrapping `ErrNoQuorum` (volume.go:77) despite the comment "is
  returned when writes must proceed without a majority". Callers cannot
  `errors.Is` on it (the shipped test's sentinel assertion is
  vacuous: `!errors.Is(err, ErrNoQuorum) && err.Error() == ""`).
  The hardcoded `chunk 0` in the message also mis-reports the index.
- `TestAttackerCancelledContextStillWrites`
  ```text
  attacker_evidence_test.go:36: WriteChunks must honor already-cancelled context
  ```
  Root cause: `WriteChunks` and `ProposeCommand` (volume.go:37, 62)
  accept `ctx` and never select on `ctx.Done()`; already-cancelled
  callers get successful writes.
- `TestAttackerProposeRaftIndexesAreIdentical`
  ```text
  attacker_evidence_test.go:58: distinct proposals must have distinct raft indexes, both=1
  ```
  Root cause: `ProposeCommand` (volume.go:68) hardcodes `idx := uint64(1)`
  for every proposal, so `Version.RaftIndex`/`OpResult.RaftIndex` never
  advance and precondition/retry ordering on raft index is meaningless.
- `TestAttackerPartialChunkBatchLeavesOrphan`
  ```text
  attacker_evidence_test.go:75: failed batch left chunk1 durable on store (orphan, non-atomic batch)
  ```
  Root cause: `WriteChunks` (volume.go:41-55) commits per-chunk and
  aborts mid-batch on the first failure, leaving earlier chunks durable
  but returning a failure. Orphaned chunk data is a quota/GC leak (low
  severity; no metadata has yet referenced it — "payload before
  reference" holds, atomicity of the batch does not).

### 2.5 `internal/metadata/locks`

- `TestAttackerCrossFamilyFalseConflict`
  ```text
  attacker_evidence_test.go:21: fcnt (distinct family) must not conflict with flock: locks: would block: held by A mode 2
  ```
  Root cause: `conflicting` (locks.go:65-75) ignores `Family`. The
  package doc states flock/fcntl/OFD are distinct families (POSIX has
  separate lock tables), but a flock-exclusive holder blocks an fcntl
  request on the same range. False conflicts that can deadlock mixed
  workloads.
- `TestAttackerBindProtoDropsInodeAndRange`
  ```text
  attacker_evidence_test.go:45: grant on different inode must succeed (BindProto ignores InodeId): locks: would block: held by owner1 mode 2
  ```
  Root cause: `BindProto` (locks.go:166-172) builds
  `Lock{Family, Owner, Mode}` only — `InodeId`, `RangeStart`,
  `RangeEnd` from `LockGrantPayload` (api/v1/gen/commands.pb.go:753+)
  are discarded, so every proto grant lands on the zero inode with an
  EOF range and collides. `LOCK_RELEASE` is a no-op in `BindProto`
  ("accept as no-ops" in metadata.go:235-237 likewise).
- `TestAttackerBlockingGrantPanicsOnSecondLock` — **segfault**
  ```text
  panic: runtime error: invalid memory address or nil pointer dereference
  [signal SIGSEGV …]
  sort.Slice({0x104460800?, …}, 0x0)
  github.com/mohamedhabas11/syncfs/internal/metadata/locks.(*Manager).usableLocked(...)
    …/locks.go:130
  github.com/mohamedhabas11/syncfs/internal/metadata/locks.(*Manager).BlockingGrant(…)
    …/locks.go:111
  ```
  Root cause: `usableLocked` (locks.go:130) calls
  `sort.Slice(m.byID[inode], nil)` — a nil less function. With ≥2 locks
  on the inode (two compatible shared `BlockingGrant`s), the sort
  dereferences the nil comparator and crashes the process. The first
  (single) lock never triggers it, which is why existing tests miss it.
- `TestAttackerBlockingGrantStarvedByReleaseSteal`
  ```text
  attacker_evidence_test.go:102: waiterB starved: Release popped/freed queue head (C stole it); B never acquired
  ```
  Root cause: `Release` (locks.go:144-147) pops the queue head and
  closes its `ready` channel — but `BlockingGrant` (locks.go:95-118)
  never selects on `ready`; it polls on a 1 ms ticker and only the
  *current* head may try. After `Release`, head waiter B is already
  dequeued, C becomes head and acquires, and B's head-check
  (`len(q) > 0 && q[0] == w`) can never hold again — B starves forever
  despite the lock being free between grants (and likewise
  `BlockingGrant` has no context/deadline parameter at all, so a starved
  waiter cannot be cancelled).
- `TestAttackerSameOwnerOverlappingUpgradeBlocked`
  ```text
  attacker_evidence_test.go:119: same-owner overlapping upgrade must succeed (merges), got: locks: would block: held by me mode 1
  ```
  Root cause: `Try`'s idempotency short-circuit (locks.go:82-84) only
  matches an identical (owner,start,end,mode) tuple; an overlapping
  same-owner re-lock conflicts with itself. POSIX fcntl same-process
  locks merge/convert instead of self-blocking.

### 2.6 `internal/auth`

- `TestAttackerEnrollmentConcurrentDoubleSpend`
  ```text
  attacker_evidence_test.go:75: concurrent single-use must succeed exactly once, got 2 successes (errs=[<nil> <nil>])
  ```
  Root cause: `Consume` (enrollment.go:77-92) is check-then-act across
  two store calls (`Get`, validate, `MarkConsumed`), each individually
  locked but not atomic jointly. Two concurrent consumers both observe
  `ConsumedAt==zero` and both `MarkConsumed`, which (enrollment.go:
  57-67) overwrites unconditionally. With a wrapper store that captures
  `Get` before a rendezvous barrier (deterministically), both calls
  succeed — the one-time enrollment is double-spendable. The shipped
  single-threaded test passes, so this is concurrency-only.
- `TestAttackerEnrollmentEmptyProofAccepted`
  ```text
  attacker_evidence_test.go:85: empty fingerprint+nonce must be rejected
  ```
  Root cause: `MemoryStore.Put` (enrollment.go:40-48) validates neither
  the fingerprint nor the 32-byte nonce, and `Consume`'s equality check
  then admits `""`/`""`. A record created with empty material enrolls
  with no secret at all (guessing not required).

### 2.7 `internal/fusefs`

- `TestAttackerRenameFileOntoDir`
  ```text
  attacker_evidence_test.go:19: rename file onto dir must fail (EISDIR/ENOTDIR), got success; d is now clobbered
  ```
  Root cause: `Rename` (posix.go:136-146) deletes the src entry and
  unconditionally overwrites the dst entry without kind checks,
  clobbering a directory with a regular file (and silently discarding
  the dir's subtree handle).
- `TestAttackerNamesWithSlashAndDots`
  ```text
  attacker_evidence_test.go:27: CreateFile("a/b") must fail (invalid component)
  attacker_evidence_test.go:27: CreateFile("..") must fail (invalid component)
  attacker_evidence_test.go:27: CreateFile(".") must fail (invalid component)
  attacker_evidence_test.go:27: CreateFile("") must fail (invalid component)
  attacker_evidence_test.go:27: CreateFile("x\x00y") must fail (invalid component)
  ```
  Root cause: `createNode` (posix.go:14-27) does no component
  validation, unlike metadata's `validName`.
- `TestAttackerBareUserXattr`
  ```text
  attacker_evidence_test.go:36: bare user. key (empty suffix) must be rejected
  ```
  Root cause: `xattrName` (xattr.go:30-38) accepts any `user.`-prefixed
  string, including the bare `"user."`. Linux requires a non-empty
  suffix after the namespace dot.
- `TestAttackerWriteOverflowPanics`
  ```text
  attacker_evidence_test.go:47: WriteFile at overflowing offset panicked: runtime error: growslice: len out of range
  ```
  Root cause: `WriteFile` (posix.go:57-85) computes
  `end := at + int64(len(data))` with no overflow or
  `EFBIG`/`ENOSPC` bound, then `append`s `end-len` zero bytes.
  `at = MaxInt64-1` over/underflows `end` and panics (`growslice` /
  `makeslice: len out of range` depending on the exact offset);
  large-but-non-overflowing offsets attempt multi-GB allocations.
  `Truncate` has the same unbounded-growth shape (posix.go:100-104).
- `TestAttackerXattrDoesNotBumpMtime`
  ```text
  attacker_evidence_test.go:81: SetXattr must update mtime/ctime (before=… after=… identical …)
  ```
  Root cause: `SetXattr` (xattr.go:57) contains the literal self
  assignment `ns.mtime = ns.mtime` (also flagged by `go vet`), so xattr
  mutation leaves mtime/ctime unchanged.

### 2.8 `internal/transport`

- `TestAttackerControlCapBypass` (low)
  ```text
  attacker_evidence_test.go:23: control-size frame of 100KiB must be rejected (MaxControlFrame=65536)
  ```
  Root cause: `MaxControlFrame`/`MaxManifestFrame` (framing.go:49-51)
  are dead constants — `maxFrameSize = MaxManifestFrame` and neither
  `WriteFrame` nor `ReadFrame` takes a stream class, so the advertised
  64 KiB control cap is unenforceable and a 100 KiB "control" frame is
  accepted. (Arguably the constants are documentation-only; then they
  should be removed or enforced.)
- `TestAttackerZeroLengthFrame` (low)
  ```text
  attacker_evidence_test.go:33: zero-length frame must be rejected (empty protobuf zero-value)
  ```
  Root cause: `ReadFrame` (framing.go:76-90) accepts `n == 0` and
  `proto.Unmarshal`s an empty buffer into a zero-valued message (empty
  `Hello` / empty `Command`) instead of rejecting the frame.
- `TestAttackerConcurrentWriteFrameInterleaves` (low)
  ```text
  attacker_evidence_test.go:104: concurrent WriteFrame corrupted stream: decoded 0/8 frames
  ```
  Root cause: `WriteFrame` (framing.go:66-72) issues two `Write` calls
  (header, payload) with no internal serialization. Two goroutines
  sharing one stream interleave header/payload and corrupt the byte
  stream for every subsequent reader. Repro requires a deterministic
  rendezvous barrier in the test writer (naive concurrency did *not*
  reproduce it — see §3). Streams should either be single-writer by
  construction (documented/enforced) or `WriteFrame` should hold a
  per-stream mutex.

  Note: `TestAttackerTLSNoClientCertRejected` failed its strong
  assertion ("`Dial` must synchronously fail") because QUIC `Dial`
  returns after the client-side TLS 1.3 handshake before the server has
  validated the client certificate — but the server's `Accept` never
  accepted the connection within 2 s, so **no application-level mTLS
  bypass was demonstrated**. This is recorded under §3 as survived-with-
  nuance, not as a confirmed vulnerability; the real lesson is callers
  must treat `Dial` success as unauthenticated until the server-side
  `Accept` returns.

## 3. Surviving attacks (held — what was tried and why)

- **Chunkstore double-open**: `TestAttackerDoubleOpenSameDirIsRefused`
  — second `Open` on the same directory is refused (Pebble LOCK). PASS.
  The store is correctly single-writer.
- **Chunkstore concurrent Put/Get**: 16 goroutines × 20 Puts each with
  immediate `Get` + byte equality — PASS under host and `-race`. The
  `s.mu` serialization is sound; no lost/corrupt chunks under
  concurrency (as opposed to on-disk damage, which is where §2.1 breaks).
- **Volume concurrent WriteChunks/ProposeCommand**: 8 × 20 mixed
  writes/proposals with unique names/opIDs — PASS under `-race` after
  fixing the test's own name collisions. The coordinator mutex holds.
- **Auth SAN bypass battery**: `TestAttackerSANBypassAttempts` exercised
  userinfo (`spiffe://syncfs.cluster@evil.example/…`), uppercase scheme,
  port suffix, empty segments, case-mutated type, `..` traversal, and
  wrong host — all rejected by `ParseSAN`/`IdentityOf`. PASS. Root
  cause of holding: exact prefix + strict `strings.Split` count +
  type whitelist.
- **Auth CA-as-leaf**: `TestAttackerVerifyCAAsLeafAttempt` — presenting
  the CA DER itself as the peer certificate is rejected by
  `leaf.Verify`. PASS. (And without the CA private key a TLS proof of
  possession is impossible.)
- **Transport TLS downgrade**: `TestAttackerTSDowngradeRejected` — a
  TLS-1.2-capped client fails against the TLS-1.3-only server config.
  PASS. Wrong-cluster dials likewise fail (existing
  `TestWrongClusterRejected`). Oversize frames are rejected cleanly
  (existing `TestFrameTooLarge`); a `0xFFFFFFFF` length header is
  rejected before allocation (`ReadFrame` checks the cap before
  allocating).
- **No-cert client (nuance, §2.8)**: `Dial` returning success is a
  QUIC/TLS-1.3 client-side artifact (server-side `Accept` never fired),
  so it is *not* evidence of an authorization bypass as written.
- **Metadata shipped tests** (`TestAtomicityOnError`,
  `TestIdempotentOpID`, `TestPreconditionStaleFails`,
  `TestRenameMovesAtomically`) all still pass — the §2.3 failures are
  in paths those tests don't cover (missing parent, rename-to-slash,
  post-unlink access), not in the covered ones.
- `-race` runs across all 8 packages: no data-race reports; the
  enrollment double-spend is a cross-call TOCTOU, not a data race.

## 4. Proposed patch list (file:line prose)

**`internal/chunkstore/chunkstore.go`**
- §2.1a — `scanPack` (~127): verify BLAKE3 (not just CRC) per record and
  refuse to *truncate-away* indexed records. Truncation must be limited
  to bytes beyond the last indexed record (a true torn tail); if a
  CRC/BLAKE3 failure occurs at or before an indexed offset, return a
  loud corruption error instead of truncating. Alternatively rebuild
  the Pebble index from validated pack records on open so `Has` and the
  pack agree.
- §2.1b — `Put` (~172): on index hit, read-back-verify the chunk
  (BLAKE3) before returning the receipt; on mismatch, rewrite the pack
  record (self-heal) or return `ErrCorrupt`.

**`internal/raftstore/wal/wal.go`**
- §2.2a — `SaveReady` (~207) + `appendEntryLocked` (~152): validate
  `e.Index == next` *before* any disk write or in-memory append.
  On gap, return `ErrIndexGap` with zero durable/mutation side effects.
- §2.2b — `Open`/`replay` (~91): load the snapshot via
  `snapshot.ReadSnapshotFile` (when present) before replaying wal.log,
  and reconcile: skip log entries ≤ snapshot index. Currently the
  snapshot file is write-only.
- §2.2c — `Compact` (~348): make compaction durable (rewrite wal.log
  without compacted entries, or persist a compaction marker replay
  honors) and reject `compactIndex > LastIndex` instead of silently
  dropping everything.
- §2.2d — `replay` (~110): distinguish tail-torn (truncate+success)
  from mid-log corruption (fail `Open` with `ErrCorrupt`/wrap
  `ErrTornRecord` without truncation). Never silently drop committed
  entries.

**`internal/metadata/metadata.go`**
- §2.3a — `doLink` (~312) and `doRename` (~358): check both parents
  exist and are dirs *before* touching `m.dentries`; return
  `ErrNotDir`/`ErrNotFound` (never assign into a nil map).
- §2.3b — `validName` (~404): also reject `"."` and `".."`; call it
  for `SrcName`/`DstName` in `doRename` (and link names) and for the
  link name in `doLink`.
- §2.3c — `doUnlink` (~340): add a link count to `Entry`; when the
  last link is removed, delete the inode (or tombstone it) so later
  commands fail `ErrNotFound`.

**`internal/volume/volume.go`**
- §2.4a — `WriteChunks` (~52): wrap the failure in `ErrNoQuorum`
  (`fmt.Errorf("%w …")`), report the actual failing chunk index `i`
  (not constant `0`), and fix the shipped test's vacuous sentinel check.
- §2.4b — `WriteChunks`/`ProposeCommand` (~37, ~62): honor `ctx`
  (`select { <-ctx.Done(): … }` before fan-out and during it).
- §2.4c — `ProposeCommand` (~68): replace `idx := uint64(1)` with the
  fsm's next raft index (monotonic version) so distinct proposals get
  distinct `RaftIndex` values.
- §2.4d (optional, low) — either make `WriteChunks` batch-atomic
  (stage-then-commit) or document that partial batches leave orphans
  and rely on chunk GC to reclaim them.

**`internal/metadata/locks/locks.go`**
- §2.5a — `conflicting` (~65): add a `Family` equality check before
  range/mode evaluation (flock/fcntl/OFD are independent tables).
- §2.5b — `BindProto` (~166): copy `InodeId`, `RangeStart`, `RangeEnd`
  from `LockGrantPayload` into the `Lock`, and implement the
  `LOCK_RELEASE` branch (currently both branches drop fields). Wire
  `EncodeProto`/`LOCK_GRANT` through `metadata` instead of the no-op.
- §2.5c — `usableLocked` (~130): remove the `sort.Slice(_, nil)` call
  (nil less crashes on ≥2 elements) or supply a real ordering key
  (owner,start). This is the segfault.
- §2.5d — `BlockingGrant`/`Release` (~95, ~133): remove the
  "Release pops the head unconditionally" steal — only the waiter that
  actually acquires should leave the queue; make `Release` wake waiters
  by re-checking, not dequeueing. Add a `context.Context`/timeout
  parameter so queued grants are cancellable (currently unbounded
  busy-wait with a 1 ms ticker and no exit path).
- §2.5e — `Try` (~78): if an overlapping lock is held by the *same*
  owner, merge/convert (POSIX fcntl semantics) or document flock-style
  semantics explicitly; don't self-WouldBlock.

**`internal/auth/enrollment.go`**
- §2.6a — `Consume` (~77) / `MemoryStore.MarkConsumed` (~57): add an
  atomic compare-and-set ("consume if unconsumed") primitive to
  `EnrollmentStore` and have `Consume` use it; `MarkConsumed` must
  refuse to overwrite a non-zero `ConsumedAt`. One-time use must be a
  single atomic step, tested with the concurrent-double-spend test in
  §2.6.
- §2.6b — `Put` (~40): reject empty fingerprint, empty/oversize nonce
  (require 32-byte hex nonce per the doc comment), zero `ExpiresAt`,
  and validate node ID charset.

**`internal/fusefs/posix.go`, `xattr.go`, `fusefs.go`**
- §2.7a — `Rename` (posix.go:136): refuse when src/dst kinds clash
  (`EISDIR` for file-onto-dir, `ENOTDIR` for dir-onto-file, `ENOTEMPTY`
  for non-empty dir replacement) instead of blind overwrite.
- §2.7b — `createNode` (posix.go:14): validate components (reject `""`,
  `"."`, `".."`, names containing `/` or `\x00`, `>255`) — reuse
  metadata's `validName` semantics.
- §2.7c — `xattrName` (xattr.go:30): require a non-empty suffix after
  `user.` (and reject embedded `\x00`); enforce a value-size limit.
- §2.7d — `WriteFile`/`Truncate` (posix.go:57, 87): overflow-check
  `at + len`, return `EINVAL`/`EFBIG` before any allocation, and cap
  growth with `ENOSPC` when the slice would exceed a sane quota.
- §2.7e — `SetXattr` (xattr.go:57): replace `ns.mtime = ns.mtime`
  with `ns.mtime = time.Now()` (ctime) — fixes both behavior and the
  `go vet` self-assignment warning.
- `errnoFor` (fusefs.go:25, low): add the missing POSIX errnos
  (`ENODATA`, `EBADF`, `EFBIG`, `EDQUOT`, `ENAMETOOLONG`, `ELOOP`,
  `ESPIPE`, `EROFS`, …) or document the `EIO`-fallback policy. Note
  `xattr.go` already returns `ENODATA` directly, bypassing the table.

**`internal/transport/framing.go`, `session.go`**
- §2.8a — `framing.go:49`: either enforce `MaxControlFrame` (thread a
  `StreamClass`-derived cap through `WriteFrame`/`ReadFrame`) or delete
  the dead constant to avoid advertising a cap that doesn't exist.
- §2.8b — `ReadFrame` (~76): reject `n == 0`.
- §2.8c — `WriteFrame` (~58): make header+payload atomic per stream
  (single `Write` of the coalesced frame, or document single-writer
  streams / add a locked stream wrapper). Do not assume concurrent
  `WriteFrame` on one stream is safe.
- §2.8d — `Dial` (session.go:69): document that QUIC `Dial` returning
  does not imply server-side client-cert verification completed; the
  server must gate all application handling on `Accept` (it does), and
  clients must gate authorization on server `Accept` + stream success,
  not on `Dial` alone. No code fix required if that contract is kept;
  the test's strong "Dial must fail" expectation is not a property
  QUIC/TLS-1.3 provides.

## Methodology notes

- Attacks were prioritized per the interface-first order (chunkstore →
  WAL → metadata → volume → locks → auth → fusefs → transport) and run
  first on the Go 1.26 host binary, then re-verified with
  `docker compose run --rm node-a` on Linux (same red output).
- All tests use `t.TempDir()` and construct inputs through the public
  `(Open/Apply/SaveReady/BindProto/Consume/WriteFile/WriteFrame/…)
  interfaces — no production code was modified.
- Coverage gaps (not exercised): crash-during-Put/ SaveReady via the
  `Hooks` fault-injection path (WAL hooks fire correctly per shipped
  tests, but no kill-mid-fsync chaos run was performed); kernel-level
  FUSE mount round-trip on Linux beyond the existing smoke test;
  multi-process (vs. multi-goroutine) lock/enrollment contention; Pebble
  corruption at the SST level (tested at the pack-file level instead).

---

## Round N — Re-verification after partial fixes (2026-11-08)

**Tester scope:** adversarial re-verification only; no production code
edited by this tester. **Baseline:** `git HEAD a915888` ("M4-02[…]; M4-03[…]",
committed mid-session by another agent) + uncommitted docs-only changes
(CHANGELOG/Makefile/docs/* — none touch the 8 target packages' production
files; confirmed by `git status`/`git diff --stat` at test time).
**Toolchain:** Go 1.26.0 (`…/go1.26/bin/go`).
**Checked-in tests alone:** `go test ./... -race -count=1` → **PASS** (all
22 packages ok, no `DATA RACE`). **Attacker runs:** 9 packages re-run under
`-race`, **0 × `DATA RACE`** — all failures below are logic/contract
violations, matching the prior round's expectation.

**Attacker files left in tree (untracked, `*_attacker_test.go` named
`attacker_evidence_test.go`):**
`internal/chunkstore/attacker_evidence_test.go`,
`internal/raftstore/wal/attacker_evidence_test.go`,
`internal/metadata/attacker_evidence_test.go`,
`internal/volume/attacker_evidence_test.go`,
`internal/metadata/locks/attacker_evidence_test.go`,
`internal/auth/attacker_evidence_test.go`,
`internal/fusefs/attacker_evidence_test.go`,
`internal/transport/attacker_evidence_test.go`,
`internal/client/cache/attacker_evidence_test.go`.
(Note: an earlier set of these files was swept by a concurrent
commit/clean mid-session; the files above are the recreated set, re-run
against HEAD `a915888`, and present at report time.)

### Round N verdict summary

| Target | Prior findings | Now PASS (fixed) | STILL FAIL |
|---|---|---|---|
| `internal/chunkstore` | 2 | 0 | **2** |
| `internal/raftstore/wal` | 4 | 0 | **4** |
| `internal/metadata` | 5 | 0 | **5** (+3 new-edge fails) |
| `internal/volume` | 4 | 0 | **4** |
| `internal/metadata/locks` | 5 | **5** | 0 (— task's "partial fix" had in fact landed fully incl. `BindProto` field copy + `LOCK_RELEASE` + `tryLocked` purge) |
| `internal/auth` | 2 | 0 | **2** |
| `internal/fusefs` | 5 | **2** (overflow guards, mtime) | **3** (rename clobber, names-NUL/length remnant, bare xattr) (+1 new-edge fail) |
| `internal/transport` | 3 | 0 | **3** |
| `internal/client/cache` (new) | — | — | **1 new-edge fail** |

Total: 30 prior findings → **23 STILL FAIL**, **7 now PASS** (locks 5/5 +
fusefs 2/5). 5 NEW edge cases added (4 fail + 1 partial — see §N.3).

### N.2 STILL FAIL (verbatim test + red output + root cause file:line)

**chunkstore (2/2 STILL FAIL)** — `internal/chunkstore/chunkstore.go` unchanged.
- `TestAttackerMiddleCorruptionDropsLaterDurableChunk` → `attacker_evidence_test.go:54: later durable chunk destroyed by earlier-record corruption: Get err=chunkstore: data corrupt Has=true` — `scanPack` (L127-151) still truncates at the first bad record mid-pack, amputating the durable chunk 2 while `Has=true`.
- `TestAttackerPutAfterCorruptionClaimsSuccessWithoutHealing` → `…:85: Put returned durable receipt for corrupt chunk but Get fails: chunkstore: data corrupt` — `Put` (L172-176) still returns success from the Pebble index without read-back verification.

**WAL (4/4 STILL FAIL)** — `internal/raftstore/wal/wal.go` unchanged.
- `TestAttackerGapWritePoisonsDurableLog` → `…:33: reopen bricked after rejected gap write: wal: entry index gap (got 5 want 1)` — `writeRecord` (L165) still persists before `appendEntryLocked` (L152) validates.
- `TestAttackerSnapshotNotRestoredOnReopen` → `…:71: snapshot lost on reopen: snapshot is temporarily unavailable` — `Open`/`replay` (L91-131) still never call `snapshot.ReadSnapshotFile`.
- `TestAttackerCompactUndoneByReopen` → `…:115: compacted entries resurrected after reopen: Entries(3) len=3 err=<nil>` — `Compact` (L348) still memory-only.
- `TestAttackerMiddleCorruptionSilentlyTruncatesCommittedEntries` → `…:159: silent truncation: reopen succeeded with LastIndex=0 (want 3 or a hard error)` — `replay` (L110-129) still truncates+successes on mid-log damage.

**metadata (5/5 STILL FAIL)** — `internal/metadata/metadata.go` unchanged.
- `TestAttackerLinkToMissingParentPanics` → `…:36: LINK to missing parent panicked: assignment to entry in nil map` — `doLink` (L312-338) still missing parent check; `m.dentries[parent]` nil write (L335).
- `TestAttackerRenameToMissingDstParentPanics` → `…:61: RENAME to missing dst parent panicked: assignment to entry in nil map` — `doRename` (L358-378) still no dst-parent check (L374).
- `TestAttackerDotNamesAccepted` → `…:84: CREATE name "." must be rejected` + `…:84: CREATE name ".." must be rejected` — `validName` (L404-409) still only checks `""`, `>255`, `/`, NUL.
- `TestAttackerRenameAcceptsSlashInDstName` → `…:99: RENAME to slash name must be rejected` — `doRename` still no `validName` on src/dst names.
- `TestAttackerUnlinkLeavesZombieInode` → `…:122: write/truncate of unlinked (last-link) inode must be NotFound` — `doUnlink` (L340-356) still no link count / inode free.

**volume (4/4 STILL FAIL)** — `internal/volume/volume.go` unchanged.
- `TestAttackerNoQuorumErrorIsSentinel` → `…:39: error must wrap ErrNoQuorum for errors.Is, got: volume: chunk 0 durable on 1/2 voters (need 2); refusing before commit` — bare `fmt.Errorf` (L52), never wraps `ErrNoQuorum` (L77).
- `TestAttackerCancelledContextStillWrites` → `…:50: WriteChunks must honor already-cancelled context` — no `ctx.Done()` select in `WriteChunks` (L37)/`ProposeCommand` (L62).
- `TestAttackerProposeRaftIndexesAreIdentical` → `…:78: distinct proposals must have distinct raft indexes, both=1` — `idx := uint64(1)` (L68).
- `TestAttackerPartialChunkBatchLeavesOrphan` → `…:104: failed batch left chunk0 durable on store (orphan, non-atomic batch): err=volume: chunk 0 durable on 0/3 voters (need 2); refusing before commit` — per-chunk commit + mid-batch abort (L41-55).

**locks (5/5 now PASS)** — fix fully landed & verified:
family check `conflicting` (locks.go:67 `a.InodeID != b.InodeID || a.Family != b.Family`);
same-owner supersede (L74-78 + `Try` purge `purgeSameOwnerOverlapLocked` L102-118);
`usableLocked`/`sort.Slice(nil)` deleted; `Release` (L169-187) nudges (select/default close) without dequeuing;
`BindProto` (L205-211) now `copy(ino[:], p.InodeId)` + `Start/End/Mode` and handles `LOCK_RELEASE` (L212-228) — this exceeded the task's "partial fix" brief (which described BindProto as unfixed), which is why this round verifies the actual tree.
- `TestAttackerCrossFamilyFalseConflict` PASS, `TestAttackerBindProtoDropsInodeAndRange` PASS,
  `TestAttackerBlockingGrantPanicsOnSecondLock` PASS, `TestAttackerBlockingGrantStarvedByReleaseSteal` PASS,
  `TestAttackerSameOwnerOverlappingUpgradeBlocked` PASS. All also pass under `-race`.

**auth (2/2 STILL FAIL)** — `internal/auth/enrollment.go` unchanged.
- `TestAttackerEnrollmentConcurrentDoubleSpend` → `…:72: concurrent single-use must succeed exactly once, got 2 successes (errs=[<nil> <nil>])` — `Consume` (L77-92) check-then-act + unconditional `MarkConsumed` (L57-67); deterministic barrier store, no `DATA RACE` (cross-call TOCTOU).
- `TestAttackerEnrollmentEmptyProofAccepted` → `…:85: empty fingerprint+nonce must be rejected` — `Put` (L40-48) still no validation; equality check admits `""`/`""`.

**fusefs (2 now PASS, 3 STILL FAIL — file partially fixed).**
- STILL FAIL `TestAttackerRenameFileOntoDir` → `…:25: rename file onto dir must fail (EISDIR/ENOTDIR), got success` — `Rename` (posix.go:161-177) validates *names* (L164-169) but still blindly `delete(src); children[dst]=ns` (L174-175) with no kind check.
- STILL FAIL (reduced) `TestAttackerNamesWithSlashAndDots` → `…:34: CreateFile("xNULy") must fail` + `…:40: CreateFile(300-byte name) must fail` (slash/dot/empty sub-cases now PASS) — `createNode` (posix.go:14) now calls `validName` (L15) but `validName` (L147-158) checks only `""`/`"."`/`".."`/`"/"`, not NUL or length. Root cause file:line `posix.go:147-158`.
- STILL FAIL `TestAttackerBareUserXattr` → `…:48: bare user. key (empty suffix) must be rejected` — `xattrName` (xattr.go:30-38) still `strings.HasPrefix(name, "user.")` only.
- NOW PASS `TestAttackerWriteOverflowPanics` — fixed: `at > 1<<62` + `len(data) > 1<<62-at` guards (posix.go:74-76), rollback on syncer error (L84-88).
- NOW PASS `TestAttackerXattrDoesNotBumpMtime` — fixed: `ns.mtime = time.Now()` (xattr.go:57).

**transport (3/3 STILL FAIL)** — `internal/transport/framing.go` unchanged.
- `TestAttackerControlCapBypass` → `…:17: control-size frame of 100KiB must be rejected (MaxControlFrame=65536)` — dead constants `MaxControlFrame`/`MaxManifestFrame` (L49-51); single `maxFrameSize`.
- `TestAttackerZeroLengthFrame` → `…:28: zero-length frame must be rejected (empty protobuf zero-value)` — `ReadFrame` (L76-90) still accepts `n == 0`.
- `TestAttackerConcurrentWriteFrameInterleaves` → `…:100: concurrent WriteFrame corrupted stream: decoded 0/2 frames (err=proto: cannot parse invalid wire-format data)` — two `Write` calls (L68/L71) with no serialization; deterministic `gateWriter` in the test.

### N.3 NEW edge cases (missed by prior round — 4 confirmed holes + 1 hygiene note)

1. **NEW `TestAttackerRenameOverwritesExistingDstSilently` (metadata, NEW) — FAIL** →
   `…:153: RENAME onto existing name silently overwrites without retiring dst inode (no EEXIST, no inode free)` —
   `doRename` (metadata.go:358-378) overwrites the dst dentry and leaks the dst inode (same zombie class as §2.3c, different trigger path).
2. **NEW `TestAttackerDoSetXattrAcceptsBareKey` (metadata, NEW) — FAIL** →
   `…:169: SET_XATTR with bare/invalid key "user." must be rejected` (also `"user"`, `""`, `"x"` all accepted) —
   `doSetXattr` (metadata.go:395-397) is a `return nil` stub with zero key validation at the state-machine layer.
3. **NEW `TestAttackerTruncateUnboundedGrowth` (metadata, NEW) — FAIL** →
   `…:185: TRUNCATE to 1<<62 bytes must be rejected (unbounded growth)` — `doTruncate` (metadata.go:295-310) assigns `e.Size` with no cap/quota.
4. **NEW `TestAttackerTruncateUnboundedGrowth` (fusefs, NEW) — FAIL** →
   `…:80: TRUNCATE to 64MiB must be rejected (no quota cap)` — `Truncate` (fusefs/posix.go:88-107 → :100-104) still `make([]byte, size-len)` unbounded; note `WriteFile` is guarded but `Truncate` is not.
   (Hygiene note: `posix.go` still allows *non-overflow* absurd offsets — e.g. `1<<40` passes the `1<<62` guard and allocates ~1 TiB. Not exercised in the test run to avoid OOM; recommend a real quota, not just `1<<62`.)
5. **NEW `TestAttackerInvalidateAllKeepsLeases` (client/cache, NEW) — FAIL** →
   `…:25: InvalidateAll must fence stale leases (Put after drop must be refused)` — `InvalidateAll` (cache.go:58-62) resets only `entries`, not `leases`; `Put` (L32-40) still authorizes on stale lease IDs after control drop (violates Inv.6 fencing).

### N.4 Executable exit criteria

Edge-case benchmarks are **meaningful only after** the following attacker
tests pass; until then benchmark numbers measure broken paths (durability,
atomicity, single-use) that masks faults (see caveat under Criterion 0).
Run: `go test ./internal/<pkg>/ -run 'TestAttacker' -count=1 -race` must be
green for every listed test, and `go test ./... -race -count=1` must stay green.

- **Criterion 0 — harness floor (prerequisite for all others):**
  `go test ./... -race -count=1` passes on checked-in tests alone (verified
  PASS at HEAD a915888; keep it green) with **zero `DATA RACE`**; all
  attacker tests below are deterministic (barriers/gates, no sleeps except
  bounded `time.After` waiters) and must pass `-race -count=1` without flakes.
- **Criterion 1 — durability (chunkstore+WAL):**
  `TestAttackerMiddleCorruptionDropsLaterDurableChunk`,
  `TestAttackerPutAfterCorruptionClaimsSuccessWithoutHealing`,
  `TestAttackerGapWritePoisonsDurableLog`,
  `TestAttackerMiddleCorruptionSilentlyTruncatesCommittedEntries`,
  `TestAttackerSnapshotNotRestoredOnReopen`,
  `TestAttackerCompactUndoneByReopen`.
- **Criterion 2 — metadata atomicity/namespace:**
  `TestAttackerLinkToMissingParentPanics`,
  `TestAttackerRenameToMissingDstParentPanics`,
  `TestAttackerDotNamesAccepted`,
  `TestAttackerRenameAcceptsSlashInDstName`,
  `TestAttackerUnlinkLeavesZombieInode`,
  `TestAttackerRenameOverwritesExistingDstSilently`,
  `TestAttackerDoSetXattrAcceptsBareKey`,
  `TestAttackerTruncateUnboundedGrowth` (metadata).
- **Criterion 3 — volume coordination:**
  `TestAttackerNoQuorumErrorIsSentinel`,
  `TestAttackerCancelledContextStillWrites`,
  `TestAttackerProposeRaftIndexesAreIdentical`,
  `TestAttackerPartialChunkBatchLeavesOrphan`.
- **Criterion 4 — locks (already met):**
  `TestAttackerCrossFamilyFalseConflict`,
  `TestAttackerBindProtoDropsInodeAndRange`,
  `TestAttackerBlockingGrantPanicsOnSecondLock`,
  `TestAttackerBlockingGrantStarvedByReleaseSteal`,
  `TestAttackerSameOwnerOverlappingUpgradeBlocked` — **ALL PASS at HEAD
  a915888**; keep green (do not regress BindProto/tryLocked).
- **Criterion 5 — auth single-use:**
  `TestAttackerEnrollmentConcurrentDoubleSpend`,
  `TestAttackerEnrollmentEmptyProofAccepted`.
- **Criterion 6 — fusefs (partially met: overflow+mtime PASS):**
  `TestAttackerRenameFileOntoDir`, `TestAttackerNamesWithSlashAndDots`
  (incl. NUL + >255), `TestAttackerBareUserXattr`,
  `TestAttackerTruncateUnboundedGrowth` (fusefs),
  `TestAttackerWriteOverflowPanics`, `TestAttackerXattrDoesNotBumpMtime`.
- **Criterion 7 — transport:**
  `TestAttackerControlCapBypass`, `TestAttackerZeroLengthFrame`,
  `TestAttackerConcurrentWriteFrameInterleaves`.
- **Criterion 8 — cache coherence:**
  `TestAttackerInvalidateAllKeepsLeases`.

**Benchmark-gate note:** benchmark only after Criteria 1–3, 5–8 pass; a
data-race-free green run is necessary but not sufficient — the exit criteria
are the logic-attacker tests above, not the race detector.
