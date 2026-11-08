# Critic findings — SyncFS

> **CLEARED (2026-11-09):** Mechanical findings (9 CRITICAL + 9 MAJOR in §§1–5)
> are addressed in the loop (commits `3cbdc5f`, `18e2780`, `b649526`, G5–G9 + Batch B–D below) and verified by `go vet` + `go test ./... -race` green + `TestEdge` C1–C8 + chaos harness. Remaining integration items (Raft-commit write path, Inv.5 lock/lease quorum, BindProto daemon wiring, GC Delete caller pins, CLI execution) are tracked as PARTIAL in `docs/known-limitations.md` (#10, #14–#17) and `docs/spec/status.md` — not claimed as fixed here. This file is retained as history; the live register is `docs/known-limitations.md`.

- Repo: `/Users/mohamed/syncfs`
- Commit: `0fef618f7897b12e39b32557d99240d8ac1f12a2` (HEAD at audit start; baseline `55344ed` for M1-M4 claims checked in parallel)
- Date: 2026-11-08
- Reviewer: CRITIC (subagent, read-only; no spec/code files edited)
- Scope read: `PROGRESS.md`, `docs/spec/status.md`, `docs/known-limitations.md`, `.spec/technical_spec.md`, `CHANGELOG.md`, full `internal/**`, `cmd/syncfs/main.go`, `api/v1/*.proto`, `docs/posix-compat.md`, `docs/compatibility.md`, `docs/reference/cli.md`, `docs/benchmarks/*`
- Tests run (Docker, `golang:1.26`, host Go is `1.15.6` and **cannot** build this repo):
  - `go test ./... -race -count=1` → **all 20 packages PASS** with original tests only
  - `go test ./internal/... -count=1` with untracked `attacker_evidence_test.go` helpers present → **30+ attacker tests FAIL**, panics confirmed. All attacker files are **untracked** (`git status ?? internal/*/attacker_evidence_test.go`, 8 files). They are not part of the repo's evidence; the failures they show are real bugs hidden by the checked-in test suite.

## Executive verdict

The project **does** have M1-M4-shaped code in the right places and the in-tree tests are green. But green is heavily over-claimed:

- **9 CRITICAL** findings (durability loss, consensus divergence, panics, security bypass, FUSE data corruption) all have minimal failing repros that the checked-in suite does not include.
- `docs/spec/status.md` marks **every M1-M4 item DONE** while the implementation still uses stubs for Raft-ordered proposals, chunk-gap gating, GC deletion, CLI daemon/mount, and the fault harness. This is a **documentation dishonesty** problem, not just code debt.
- Test names that sound like proof (`TestTornWriteRecovered`, `TestThreeNodeElectAndCommit`, `TestFUSEmountSmoke`, `TestEnrollmentSingleUse`, `TestSessionExpiryDropsLocks`) either assert nothing about the property in their name or, in one case, pass *because* the bug under test fired and was discarded (`_ = m.Try(b)`).

Nothing here requires a rewrite. The fix list is brutal but tractable, ordered by severity below.

---

## 1. Real bugs (logic, concurrency, ordering)

### CRITICAL 1 — WAL snapshot is never restored; committed index resets on restart

- `internal/raftstore/wal/wal.go:91-102` `Open` replays only `wal.log`. It never calls `snapshot.ReadSnapshotFile`.
- `internal/raftstore/snapshot/snap.go:65` `ReadSnapshotFile` exists but **zero callers** (grep proves only tests call it).
- Consequence: after `SaveReady` with a snapshot, close + `Open`, `Snapshot()` returns `ErrSnapshotTemporarilyUnavailable` and `FirstIndex==1`. Raft thinks committed index went backwards. `InitialState` re-bootstraps voters from `InitialPeers` because snapshot ConfState is lost. A restarted node can double-vote or diverge.
- Evidence: `internal/raftstore/wal/attacker_evidence_test.go:42-77` `TestAttackerSnapshotNotRestoredOnReopen` **fails** with exactly `snapshot lost on reopen: snapshot is temporarily unavailable`.

**Spec violation:** §8.4 (persist snapshots before reporting applied, deliver in index order), §8.5 (snapshot envelope activation), Inv.9 (fail closed, never silently discard committed state).

**Fix:** `Open` must `ReadSnapshotFile` if present, seed `s.snapshot`, discard `ents ≤ snap.Index`, and set `hardState.Commit=max(..., snap.Index)` *and* `hardState.Term=snap.Metadata.Term` before replay. Add a write-path test: save → snap → close → reopen → assert `FirstIndex==snap.Index+1`.

### CRITICAL 2 — WAL `Compact` corrupts index math and is undone by reopen

- `internal/raftstore/wal/wal.go:348-363` `Compact` drops `s.ents` entries but **does not advance `s.snapshot`**. `Entries`/`Term`/`FirstIndex` all compute from `s.snapshot.Metadata.Index`:
  - `wal.go:283-297` `Entries(lo,hi)` uses `base=s.snapshot.Metadata.Index` and `s.ents[i-base-1]` without bounds check beyond `hi` clamping. After `Compact(2)` on `1..3`, `Entries(1,4)` should be `ErrCompacted` but returns the resurrected entries (because `Compact` never persisted).
  - `wal.go:332-336` `FirstIndex()` still returns `snapshot.Index+1` after compaction — wrong.
  - `wal.go:309-324` `Term(i)` for a compacted index returns `snapshot.Metadata.Term` even though `snapshot` is still 0.
  - Disk: `Compact` never rewrites `wal.log`; reopen replays the full original log. So `Compact` is memory-only.
- Driver never calls `Compact` (grep: zero callers), so production hits unbounded WAL growth instead. Either way broken.

**Evidence:** `TestAttackerCompactUndoneByReopen` **fails**: `compacted entries resurrected after reopen: Entries(1) err=<nil>`.

**Fix:** Persist compaction (on snapshot actually present, truncate WAL file and write a WAL header marking base index); make `Compact` require `compactIndex == snapshot.Index` or make it atomically advance a durable base. Add `Compact` + close + reopen + `Entries` test.

### CRITICAL 3 — WAL gap poisoning + silent truncation of committed entries

- `wal.go:207-218` `SaveReady` writes each entry via `writeRecord` (line 166-189) **then** validates index via `appendEntryLocked` (line 215). If `SaveReady` gets `IndexGap`, the bad record is **already fsynced to disk**. Reopen then fails identically (`wal: entry index gap (got 5 want 1)`) — daemon bricks itself. Validate before write or truncate on error.
- `wal.go:104-129` `replay`: any `decodeRecord` failure truncates from that offset to EOF and **succeeds**, even if the corrupt byte is in the *middle* of the log with valid committed entries after it. Corrupting entry 1 of `1..3` yields `LastIndex==0` after reopen, silently dropping committed entries 2-3. Inv.9 demands fail-closed (node non-voting, quarantine) not silent truncation. `TruncatedErr()` is recorded but nobody reads it (grep: no caller checks `TruncatedErr` besides tests).

**Evidence:**
- `TestAttackerGapWritePoisonsDurableLog` **fails**: `reopen bricked after rejected gap write: wal: entry index gap (got 5 want 1)`.
- `TestAttackerMiddleCorruptionSilentlyTruncatesCommittedEntries` **fails**: `silent truncation: reopen succeeded with LastIndex=0 (want 3 or a hard error)`.

**Fix:** Validate index contiguity *before* `writeRecord`; on `SaveReady` error, truncate the just-written record. On replay, distinguish torn *tail* (truncate) from middle corruption (return hard error / refuse to start as voter).

### CRITICAL 4 — Metadata `doCreate` uses `crypto/rand` inode IDs → non-deterministic state machine → leader/follower divergence

- `internal/metadata/metadata.go:267-269`:
  ```go
  newID := [16]byte{}
  _, _ = rand.Read(newID[:])
  m.inodes[newID] = &Entry{Kind: KindFile, ...}
  ```
  Same Raft entry applied on two replicas generates two different inode IDs. File header comment (line 4) says "replay is a pure function of the command stream" — this breaks it. `WriteRanges`/`Truncate`/`Link` referencing the created inode by ID will then target different objects per replica. Tests never check cross-replica determinism (single-machine name→ID lookup only).

**Spec violation:** §8.2/§8.4 deterministic apply, §4 Inv.2 (total order of committed commands).

**Fix:** Derive ID deterministically from `(OperationID, parent, name)` e.g. `SHA256(OperationID || parent || name)[0:16]`, or carry the allocated ID in the command payload. Add a two-machine replay test: apply same `CREATE` to two clones, assert same inode ID.

### CRITICAL 5 — Metadata panics on missing parent (nil-map write)

- `metadata.go:332-336` `doLink`: no parent existence/type check; `m.dentries[parent][p.Name] = target` with missing parent → `assignment to entry in nil map`.
- `metadata.go:358-377` `doRename`: `m.dentries[dstPar][p.DstName] = kid` with missing `dstPar` → same panic. `doCreate` guards parent (`ErrNotDir` at 258), `doLink`/`doRename` don't. `doUnlink` guards (line 348) but `doLink`/`doRename` don't.

**Evidence:**
- `TestAttackerLinkToMissingParentPanics` **panics**: `LINK to missing parent panicked: assignment to entry in nil map`.
- `TestAttackerRenameToMissingDstParentPanics` **panics** identically.

**Fix:** For both, validate `m.dentries[parent]` exists and `m.inodes[parent].Kind == KindDir`, return `ErrNotDir`/`ErrNotFound`. Same for `doRename` (`srcPar`/`dstPar` both dirs). A state-machine panic on follower apply = cluster brick.

### CRITICAL 6 — Locks: family ignored, `BlockingGrant` panics via `sort.Slice(nil)`

- `internal/metadata/locks/locks.go:64-75` `conflicting(a,b)` never checks `Family`. Spec §8.3 (G-1): "flock, fcntl, OFD are three distinct families and MUST NOT be conflated into one key". `byID map[[16]byte][]Lock` (line 45) keys only by inode — so an fcntl lock blocks flock and vice versa.
  - Evidence: `TestAttackerCrossFamilyFalseConflict` **fails**: `fcnt (distinct family) must not conflict with flock: locks: would block: held by A`.
- `locks.go:130` `usableLocked`: `sort.Slice(m.byID[inode], nil)` — nil Less func → **SIGSEGV**. Hits on every successful `BlockingGrant` (line 111). Checked-in tests never call `BlockingGrant`, so never seen.
  - Evidence: `TestAttackerBlockingGrantPanicsOnSecondLock` **panics** at `locks.go:130` as predicted.
- `locks.go:166-172` `BindProto` drops `InodeId`, `RangeStart/End` (builds `Lock{Family, Owner, Mode}` only, InodeID zero). Two grants on different inodes therefore conflict with each other. Evidence: `TestAttackerBindProtoDropsInodeAndRange` **fails**: `grant on different inode must succeed (BindProto ignores InodeId)`. `LOCK_RELEASE` is ignored (no release path at all).
- Also `Release` (133-148) pops/wakes the queue head while `BlockingGrant` (93-118) never listens on `w.ready`; wakeup is ticker-poll only. `ApplyReleases` doesn't wake waiters. `Try` for same-owner upgrade returns `WouldBlock` against own lock (no merge) — `TestAttackerSameOwnerOverlappingUpgradeBlocked` would fail (same-owner upgrade must merge per POSIX fcntl).

**Fix:** Key locks by `(Inode, Family)`; fix `BindProto` to populate `InodeID/Start/End/Owner/Mode` and handle `LOCK_RELEASE`; delete `sort.Slice(nil)` (or give a real comparator); same-owner merge for `Try`; real `ready` wakeup or condition variable; add `BlockingGrant` + `BindProto` tests.

### CRITICAL 7 — Chunkstore: corruption handling drops later durable chunks; dedup lies about durability

- (a) `internal/chunkstore/chunkstore.go:127-151` `scanPack` on any `readRecord` error truncates the pack from that offset onward. Corrupting record 1 therefore destroys records 2..n even though they were fsynced, indexed in Pebble, and previously receipted. There is no per-record quarantine (spec requires forensic quarantine; Pebble entries remain dangling).
  - Evidence: `TestAttackerMiddleCorruptionDropsLaterDurableChunk` **fails**: `later durable chunk destroyed by earlier-record corruption: Get err=chunkstore: data corrupt (Has=true)`.
- (b) `Put` (158-204) checks `lookupLocked` and returns success without verifying on-disk bytes. After bit-rot, a retry `Put` returns a durable receipt for corrupt bytes. `Get` then fails. The coordinator (`internal/volume/volume.go:37-57`) trusts the receipt count as quorum durability — the lie propagates to "durable on majority" claims.
  - Evidence: `TestAttackerPutAfterCorruptionClaimsSuccessWithoutHealing` **fails**: `Put returned durable receipt for corrupt chunk`.
- (c) `lookupLocked` (275-285) dereferences Pebble value `v[:4]`, `v[4:]` without length check. A torn/short Pebble value (`<12` bytes) panics instead of returning an error. `Get` also dereferences `s.packs[loc.packNumber]` without nil check after `openPack` gaps (sparse packs) → nil `*os.File` panic.
- (d) `TestTornWriteRecovered` (checked-in) actually *asserts* the torn state is tolerated (`ErrCorrupt` for chunk2) while its comment says "either durable or dropped; never corrupt-readable" — `ErrCorrupt` *is* corrupt-readable. Torn index entries are never deleted. See §3.

**Spec violation:** Inv.9 (hash mismatch → quarantine, non-voting, never silent self-heal by discarding), §7.5 (pack `[length][BLAKE3][data][fsync-mark]` + dedup race note), §8.2 step 4 (verify digest + force durable before streaming).

**Fix:** `scanPack` must not truncate beyond the first corrupt record into later valid records (or must verify each record independently per offset); `Put` dedup must `Get`+BLAKE3-verify before returning receipt, else re-write; `lookupLocked` must bounds-check; `Get` must nil-check pack handles; add torn/middle-corruption tests that assert dangling index handling.

### CRITICAL 8 — Auth: enrollment double-spend (TOCTOU), mTLS role not enforced, `VerifyPeerCertificate` vs `VerifyConnection` lie

- (a) `internal/auth/enrollment.go:77-91` `Consume` does `Get` (82-86: checks `ConsumedAt`, expiry, fingerprints) then `MarkConsumed` (91) as **two separate locked calls**. `MemoryStore.Get`/`MarkConsumed` each lock individually (enrollment.go:50-67), so two concurrent `Consume` both see `ConsumedAt==zero` and both succeed → replay enrollment. Spec §9/§12.1: one-time nonce, exactly once.
  - Evidence: `TestAttackerEnrollmentConcurrentDoubleSpend` **fails**: `got 2 successes (errs=[<nil> <nil>])`.
- (b) `internal/auth/identity.go:76` `VerifyPeerCertificateFunc` only verifies chain + cluster binding. It never calls `RoleFor` and never distinguishes node/client/admin streams. `internal/transport/session.go:23-54` uses it as `VerifyPeerCertificate` (TLS `ClientAuth: RequireAndVerifyClientCert`). A client cert can present on a raft port. `RoleFor` (`types.go:35`) is used only in `auth_test.go`, never in transport/session code (grep confirms).
- (c) Doc lie: `identity.go:74-75` says "It MUST be registered as VerifyConnection by the transport layer (never VerifyPeerCertificate alone, which is skipped on resumed connections)" — but `session.go:33,51` registers it as `VerifyPeerCertificate`. Ticket-disabling (`SessionTicketsDisabled:true` 35,52) is a real compensating control, but the code directly contradicts its own MUST. Spec §8.1: "Role validation uses VerifyConnection, not VerifyPeerCertificate alone".

**Fix:** Implement `Consume` transactionally (single lock / compare-and-mark in one critical section, `ConsumeLocked` on the store, or `sync/atomic` on `ConsumedAt`). Register `VerifyConnection` in transport (move identity checks there, keep chain verification), and enforce per-stream role mapping (raft stream ⇒ voter/learner node cert, chunk stream ⇒ node, client stream ⇒ client/admin). Reject empty fingerprint+nonce (see `TestAttackerEnrollmentEmptyProofAccepted` **fail**: empty proof accepted — `Consume` compares `""==""` via `ConstantTimeCompare` which returns 1 for two empties).

### CRITICAL 9 — FUSE: `WriteFile` discards coordinator error; `WriteFile` panic on overflow; `Lookup` returns nil inode; `Open` ignores flags

- (a) `internal/fusefs/posix.go:78-84`:
  ```go
  _ = h.syncer.OnWrite(name, at, ...)
  return uint32(len(data)), syscall.Errno(0)
  ```
  Comment says "stays fail-closed by refusing writes that return an error here" — code ignores the error and **acknowledges success**. Any M5 coordinator failure becomes a silent durability downgrade (Inv.10 violated). Fix: propagate error.
- (b) `posix.go:57-85` `WriteFile`: `end := at + int64(len(data))`; if `end > int64(len(ns.data))` it does `ns.data = append(ns.data, make([]byte, end-len...))`. No cap, no overflow check. `at = MaxInt64-1` → `makeslice: len out of range` panic (evidence: `TestAttackerWriteOverflowPanics` **panics**). FUSE daemon must never panic on kernel-controlled offsets; must return `EINVAL/EFBIG/ENOSPC`.
- (c) `internal/fusefs/fusefs.go:150-161` `Root.Lookup`: success path `return nil, syscall.Errno(0)` — nil `*fs.Inode` + success errno. go-fuse expects a valid inode (or ENOENT). `TestFUSEmountSmoke` never does `Lookup`/`ls`; it only `Stat`s the mountpoint. A real `ls`/`stat` via the kernel would dereference nil or fail unpredictably. Similarly `Root.Open` (163-169) returns `nil, FOPEN_DIRECT_IO, 0`, ignoring flags and not checking `O_DIRECT`/`O_EXCL` etc. (spec requires `O_DIRECT → EINVAL` in strict mode; code always returns DIRECT_IO without looking at flags).
- (d) `Root.Getattr` (144-147) calls `r.attrs(r.h.root)`; with `&Handler{}` (used by `TestFUSEmountSmoke`, fusefs_linux_test.go:14) `root==nil` → `n.mu.RLock` on nil → panic on first getattr. `NewHandler(volume)` ignores `volume` (fusefs.go:83-86).

**Fix:** Propagate `OnWrite` errors; cap write offsets (`MaxFileBytes` from volume spec / `EFBIG`); implement `Lookup` to return a real `fs.Inode` (or use go-fuse stable `NodeLookup`); honor open flags; guard nil root.

### MAJOR 10 — Volume coordinator is local IPC masquerading as quorum: no Raft commit, hardcoded RaftIndex=1, no lease revocation

- `internal/volume/volume.go:18-32` `Coordinator.stores map[uint64]*chunkstore.Store` — in-process pointers, not QUIC clients. `WriteChunks` (37-57) calls `s.Put` on each local store. No network, no peer identity (Receipt has no `NodeID`: `chunkstore.go:38-44` — spec §8.2 step 5 requires receipt to name the peer). Two map keys aliasing the same `*Store` would double-count one physical store as two voters.
- `ProposeCommand` (62-74) bypasses Raft entirely: `idx := uint64(1)` hardcoded (line 68), calls `fsm.Apply` directly. Comment admits "Raft-ordered proposal lands when the state machine rides Raft in M2-06" — but `docs/spec/status.md:12` marks M2-05 DONE and `TestIdempotentRetry` enshrines `RaftIndex==1`. Evidence: `TestAttackerProposeRaftIndexesAreIdentical` **fails**: two distinct proposals both have `RaftIndex==1`.
- Chunk → metadata linkage is API-optional: `WriteChunks` and `ProposeCommand` are separate methods; nothing forces `ProposeCommand` to have seen a majority receipt. A caller can `ProposeCommand(WRITE_RANGES, newManifest=garbage)` and it commits (metadata `doWriteRanges` never checks `PreparedChunks` either — see #12). Inv.3 unenforced by design.
- No lease revocation (`lease.WaitClear`, `cache.Invalidate`) is called. No context cancellation (see #14). `var ErrNoQuorum` (line 77) is never returned — `WriteChunks` returns bare `fmt.Errorf` (line 52), so `errors.Is(err, ErrNoQuorum)` fails. Evidence: `TestAttackerNoQuorumErrorIsSentinel` **fails**.
- Partial-batch orphan: if chunk 0 succeeds majority and chunk 1 fails (e.g. oversize), `WriteChunks` returns error but chunk 0 stays durable (orphan). Spec says orphans become unreachable garbage (needs GC tracking) — coordinator doesn't record them. Evidence: `TestAttackerPartialChunkBatchLeavesOrphan` **fails**.

**Spec violation:** §8.2 steps 5-8, Inv.3, Inv.5 (no quorum → no mutation).

**Fix:** Make `WriteChunks` return per-peer receipts (`PeerID` field), verify distinct peers; make `ProposeCommand` take receipts + drive Raft (`driver.Propose` → commit → `fsm.Apply`); assign real `RaftIndex` from commit; wrap `ErrNoQuorum` with `%w`; add lease revocation step; check `ctx.Err()` at entry and per-chunk.

### MAJOR 11 — Driver chunk-gap gating is a stub; voters arg ignored; apply blocks Ready loop

- `internal/raftstore/driver/driver.go:21,97-106` `chunkPresent` stored but `checkChunkGaps()` `return nil` unconditionally. `Campaign` always proceeds. Spec Inv.4/§8.4: "MUST NOT apply a command referencing an absent chunk, MUST NOT campaign while such entry is unapplied, CATCHING_UP cannot campaign or vote (except promoted-but-unapplied voter votes)". Code implements none of this. Vote grant is also ungated (it just forwards `StepMsg`). A follower missing chunks can be elected leader and then be unable to apply — the chunk-gap deadlock (§8.4 G-2 note).
- `driver.go:43` `_ = voters` — callers passing `voters` believe membership is set; it comes only from `Storage.InitialState`. If `InitialPeers` forgotten (zero ConfState), `RestartNode` panics cryptically.
- `driver.go:62-83` `Step` sends committed entries *synchronously* (`d.applyRequested <- e`, line 76). If consumer lags, `Step` blocks → heartbeats stall → violates §8.4 "MUST NOT block Heartbeat/AppendEntries on state-machine apply". No buffering, no `ctx` applied inside loop.
- `NewDriverWithTicks` missing spec-required `MaxUncommittedEntriesSize` bound (§8.4) and `TickMs/ElectionTick/HeartbeatTick` rationale has no doc reference ("chosen for LAN quorum … in M1-04" — there's no M1-04 doc pinning numbers; values 15/10/1 are in test only).

**Fix:** Wire real `checkChunkGaps` (scan committed manifests vs `chunkPresent`, gate `Campaign` and apply; still allow vote-grant per spec exception); assert non-empty ConfState at construction; buffer apply or drop-to-queue so `Step` never blocks; add a gap test: follower without chunk → `Campaign` refuses, leader missing chunk → apply defers.

### MAJOR 12 — Metadata semantic gaps: dangling manifests, zombie inodes, dot/slash names, no-op xattrs, wrong uid/gid

- `metadata.go:274-293` `doWriteRanges` copies `NewManifest` without checking `len==32` beyond (it does len check) but never checks the manifest's chunks exist (`PreparedChunks` ignored). Inv.4 violated: replica will apply a command referencing absent chunk.
- `doUnlink` (340-356) deletes the dentry but never decrements link count / removes inode. Last-link unlink leaves inode alive and `TRUNCATE` on zombie succeeds. Evidence: `TestAttackerUnlinkLeavesZombieInode` **fails** (`truncate of unlinked inode must be NotFound`, got success). No open-handle tracking either (unlink of open file must retain until close — `docs/compatibility.md:20`).
- `validName` (404-409) rejects only `""`, `>255`, `/`, NUL. Accepts `".", ".."` — `doCreate("."/"..")` succeeds (evidence: `TestAttackerDotNamesAccepted` **fails** for both). `doRename` never validates `SrcName`/`DstName` at all → `DstName="x/y"` accepted (evidence: `TestAttackerRenameAcceptsSlashInDstName` **fails**). `doLink`/rename also accept `.`/`..`.
- `doSetXattr` (395-397) `return nil` — silently "committed" without storing. `doVolumeConfig` (399-402) `m.volSpecs["quota"]++` — ignores payload (quota/max_file_bytes). `doCreate` (269) sets `UID: cmd.Role, GID: cmd.Role` — using the authorization Role enum as uid/gid (proto `role uint32` is a principal enum, not uid). No directory-create path: `doCreate` always `KindFile`; no `KindDir` creation, no symlink creation (`KindSymlink` constant unused). Tree is flat root-only.
- `doTruncate` (295-310) sets `Size` without touching `Manifest` — size/manifest diverge. No `MaxFileBytes`/quota enforcement at admission.
- `doRename` (358-377) unconditionally overwrites existing dst (no `ENOTEMPTY`/`EISDIR`/`ENOTDIR` handling) and allows moving a directory into itself (cycle). No `..` check.
- `doLink` (312-338) bumps `te.Rev++` (line 336) but never bumps parent `generations` — lease fencing via `generations` (lease.go) is never invalidated on create/link.

**Fix:** Validate `PreparedChunks` + chunk presence in `doWrite`; fix unlink link-count/GC; harden `validName` + validate rename/link names (`"."`, `".."`, `/`); implement xattr/ACL storage; carry uid/gid properly; add dir/symlink creation; bind size↔manifest; add lease-generation bumps on every dentry mutation.

### MAJOR 13 — Client journal is in-memory, `Reconnect` ignores lookup, retention broken

- `internal/client/client.go:26-72` `Journal` is a `map` with no file persistence — "durable" (line 24) is false; restart loses all op outcomes. `pruneLocked` (62-72) only prunes **one** entry when `len >= retention` and picks a *random* map victim (Go map iteration), not oldest. 10-minute TTL from spec is nowhere enforced (`at time.Time` stored but never checked — line 35 vs `Lookup` 52-60 which only checks existence). `expired` map written but never read.
- `Session.Reconnect` (103-112) checks `lookup == nil` then returns without calling `lookup`. Client never asks the server for outcome; it only `Query`s its own journal (114-117). Spec §8.2: "client asks GetOperation before retrying". A fresh (post-restart) client gets `OpUnknown, false` and must not guess — code's `Query` returns that, but nothing enforces "don't retry as fresh operation".
- `cache.InvalidateAll` (`internal/client/cache/cache.go:58-62`) clears entries but **not** `leases`. After `Session.Drop` (client.go:90-100), a subsequent `Put` with the old lease ID still succeeds at the cache layer — Inv.6 broken if anyone calls `Put` directly. `Session.Check` is the only guard, and nothing forces callers to check it.
- `internal/client/client_test.go:40` `if _, err := s.Reconnect(nil); err == nil` — test only checks the nil case, never that `lookup` is actually invoked to reconcile unknown ops.

**Fix:** Persist journal (WAL append or `syncfs` request journal per spec §5.2); prune to retention bound with real TTL sweep; call `lookup` for every `OpUnknown` on `Reconnect` and fail if any remain unresolved before `Check()` passes; clear leases on `InvalidateAll`.

### MAJOR 14 — Context/deadline completely ignored in coordinator and lock wait

- `volume.go:37,62` both `WriteChunks` and `ProposeCommand` take `ctx` but never read it. FUSE deadline (`SyncFS.RequestDeadline` default 5s, spec §8.2) can't trigger `EIO`. Evidence: `TestAttackerCancelledContextStillWrites` **fails** (cancelled ctx still writes).
- `locks.go:95-118` `BlockingGrant(l Lock) error` takes no `ctx` at all; ticker loop (101-118) is unbounded. No cancellation → goroutine leak when client times out. No deadlock detection (`EDEADLK`) — spec §8.3/compatibility.md requires it as V1 leader duty; two waiters deadlocked on each other spin forever.

**Fix:** Check `ctx.Err()` on entry and per-chunk/per-tick in coordinator; change `BlockingGrant` to `BlockingGrant(ctx, Lock)` with wait-queue deadline; implement deadlock detection (wait-for graph cycle → `EDEADLK`, not `WouldBlock`).

### MAJOR 15 — FUSE mount is flat, non-nested, lock-free, and kernel-cache unsafe

- `internal/fusefs/posix.go` operates only on `h.root.children`. `createNode` (14-27) always creates at root. There is no path resolution, no nested `mkdir` inside `mkdir`, no `forget`, no negative-dentry handling. `posix-compat.md` says directories DONE but only root level exists. Metadata (`doCreate`) is equally flat (see #12). The two flat models don't even agree.
- `KernelMount` (fusefs.go:97-103) builds `fs.Options{MaxBackground:64, FsName}` with **no** strict cache options. Spec §8.3 requires: zero/very-short metadata TTL, `EnableWritebackCache OFF`, `FOPEN_DIRECT_IO` per-open, `Forget` handling, negative dentries. `MountOpts()` (89-94) is *a different* options object from the one actually used to mount — the tested options are not the mounted options. go-fuse defaults are 1s-unsafe per spec note (M3-01).
- No lock forwarding to `metadata/locks` — `flock`/`fcntl` in FUSE adapter unimplemented though `posix-compat.md:19` claims "DONE (manager)".
- `ReadFile` returns `EISDIR` for symlinks (should be `ELOOP`-ish/`EINVAL` distinction); `Mkdir` hardcodes `uid/gid 0,0` (root) regardless of FUSE caller (permission checks absent); `Rename` blindly overwrites (file→dir clobbers dir entry — evidence `TestAttackerRenameFileOntoDir` **fails**); `createNode` accepts `"a/b"`, `".", "..", "", NUL` (evidence `TestAttackerNamesWithSlashAndDots` **fails** for all 5); `xattrName` accepts bare `"user."` (evidence `TestAttackerBareUserXattr` **fails**); `SetXattr` does `ns.mtime = ns.mtime` (xattr.go:57, no-op) so mtime never bumps (evidence `TestAttackerXattrDoesNotBumpMtime` **fails**).

**Fix:** Implement path resolution (parent inode + name components), enforce name validation at both layers, wire cache options correctly into `KernelMount`, forward locks, fix errno mapping for rename/type mismatches.

### MAJOR 16 — Lease generation never checked; same inode cached forever

- `internal/volume/lease/lease.go:15-58` `Lease{Generation}` is stored at grant but `Valid` (86-94) never compares it to the current inode generation. `Grant` (45-58) takes `generation` from caller but trusts it. Coordinator never passes the generation (no lease revocation step at all). A stale-generation lease is "valid" until expiry/revocation — reader can serve stale metadata past a mutation that should have bumped generation. Meanwhile `metadata.go` bumps `generations[parent]` only on unlink/rename (lines 354, 375-376), never on create/link — so a `Readdir` cache lease survives a new file creation.

**Fix:** `Valid` must compare against the StateMachine's current generation (pass a `currentGen` callback or store generations in lease manager); bump generations on every dentry mutation (create/link/unlink/rename); coordinator must `Revoke` + `WaitClear` before proposing.

### MAJOR 17 — `OpResult` retention unbounded; no payload binding on op-ID reuse

- `metadata.go:70,120-124,206` `ops map[[16]byte]*OpResult` grows forever. Spec §8.2: bounded N=10,000 / 10 min (`SyncFS.OpResultRetention`). No eviction.
- `Apply` duplicate check (178-180) returns `ErrCommandFailed` without comparing payloads. Reusing an op-ID with a *different* command returns the *old* result via `GetOp` — caller believes the new mutation committed when it was ignored. No payload hash stored. Idempotency requires binding op-ID to payload (else silent lost update).

**Fix:** Bound the map (FIFO with payload hash); store `protoHash(cmd)` with result; on duplicate with different hash return `ErrCommandFailed` *and* mark new op `REJECTED` (don't return old `COMMITTED`).

### MAJOR 18 — WAL file creation not dir-synced; pack index value unchecked (see also #7)

- `wal.go:92` `OpenFile(..., O_CREATE...)` never fsyncs the containing directory after creating `wal.log`. Crash before dir sync loses the file name. Spec §7.5 pattern (fsync file *and* parent dir) is applied to chunk packs (`syncDir` in Put) but not to WAL or snapshot tmp files in `Open`. `WriteSnapshotFile` (snap.go:56-60) *does* dir-sync after rename — WAL doesn't even do that for creation.

**Fix:** After creating `wal.log` (fresh file), `fsync` the dir. Same for any new pack file before first `Put` acknowledgment (partly done in Put).

---

## 2. Spec-vs-code mismatches (Invariants §4 + §8)

| # | Spec invariant | Code reality | File |
|---|---|---|---|
| S1 | Inv.3 Payload-before-reference (majority receipts before commit) | `ProposeCommand` never calls `WriteChunks`; `doWrite` never checks receipts | `volume.go:62-74`, `metadata.go:274-293` |
| S2 | Inv.4 Apply-only-with-payload; no campaign with gap | `checkChunkGaps` stub; `chunkPresent` unused | `driver.go:103-106` |
| S3 | Inv.5 No quorum no mutation/lock/lease | Coordinator has no leader/quorum check; locks/leases local-only | `volume.go:18-32`, `locks.go`, `lease.go` |
| S4 | Inv.6 Strict cache safety (invalidate before expiry/drop) | Cache `InvalidateAll` keeps leases; client `Drop` local-only | `cache.go:58-62` |
| S5 | Inv.7 Idempotency (stable op-ID, durable retained result, bounded) | In-memory unbounded maps, no payload binding, hardcoded index | `metadata.go:168-213`, `client.go:26-72` |
| S6 | Inv.8 One membership change at a time; learner never counts | `Pending` cleared on *emit* (promotion request) not commit; `BeginRemove` doesn't set pending | `member.go:86-101,104-125` |
| S7 | Inv.9 Fail closed (quarantine, non-voting) | WAL middle-corruption silent truncate; chunk torn tail silent truncate; snapshot lost | `wal.go:104-129`, `chunkstore.go:127-151` |
| S8 | Inv.10 No hidden downgrade (strict ≠ allow async/stale) | `WriteFile` swallows coordinator error → success; `Fsync` no-op | `posix.go:78-84,110-121` |
| S9 | §8.2 lease revocation + Raft commit + apply ordered | Steps 6, 8 missing entirely | `volume.go` |
| S10 | §8.3 three leases not conflated; FUSE cache TTL writeback OFF | `MountOpts` ≠ `KernelMount` opts; no TTL/Forget handling | `fusefs.go:89-103` |
| S11 | §8.3 flock/fcntl/OFD distinct families; EDEADLK | Family ignored; no EDEADLK; `BindProto` drops inode/range | `locks.go:64-75,166-172` |
| S12 | §8.4 Ready ordering; apply must not starve heartbeat | `Step` blocks on apply channel | `driver.go:74-78` |
| S13 | §8.5 snapshot envelope (version, cluster/vol, checksum) | `[sha256][len][pb]` only; no version/IDs | `snap.go:23-62` |
| S14 | §8.1 VerifyConnection + role validation | Uses `VerifyPeerCertificate`; role never checked | `session.go:24,42`, `identity.go:76` |
| S15 | §7.5 blobs `: fdatasync + fsyncdir` (WAL) | WAL create never dir-synced | `wal.go:92` |
| S16 | §7.4 command atomic (all or none) | `Apply` deep-copy/splice *is* atomic, but `doWrite`/`doTruncate` bypass chunk check; `doUnlink` zombie leaves partial state | `metadata.go:192-207` |

---

## 3. Test gaps — tests that say PASS but assert nothing meaningful

Quoted verbatim (or close) because the *name* is the lie:

- **`TestTornWriteRecovered`** (`internal/chunkstore/chunkstore_test.go:113`). Asserts chunk1 survives and chunk2 is "byte-exact or ErrCorrupt". `ErrCorrupt` *is* corrupt. It never asserts the dangling index entry is removed, that `Has(id2)` becomes false, or that a later `Put` heals. The pack truncation is never reconciled with Pebble. Green while recovery is incomplete.
- **`TestWriteChunksMinorityRefused`** (`internal/volume/volume_test.go:71`). Ends with:
  ```go
  if !errors.Is(err, ErrNoQuorum) && err.Error() == "" {
      t.Fatalf("unreachable")
  }
  ```
  `err` is non-nil (proved by the `if err == nil` above), so `err.Error()==""` is false and the branch is **dead code**. It also sets up 2 stores with quorum 2 where one is closed — a 50% loss, not a "minority of 3" (the spec case). `errors.Is(err, ErrNoQuorum)` is never actually tested (and would fail — see #10) because the `&&` saves it. **Pass trap.**
- **`TestThreeNodeElectAndCommit`** (`internal/raftstore/driver/driver_test.go:105`). Asserts *a* leader exists and *one* entry reaches the shared channel. Never asserts 2/3 commit, WAL persistence ordering, restart recovery, or snapshot/compaction. `SetApplyChannel` is set *after* `Propose` (line 124 after 121) — if commit won that race the entry would be dropped (nil-check skips) and the test would time out, so it implicitly bets on timing. The test-local `mesh` ticks one node per round (42-70) while `internal/testkit/cluster/cluster.go:26-41` `Mesh.Round` ticks *every* node per round (the pattern PROGRESS.md says deadlocked and was "fixed"). The reusable harness contradicts the fixed one. No test covers `testkit/cluster` at all (zero test files).
- **`TestFUSEmountSmoke`** (`internal/fusefs/fusefs_linux_test.go:8`). Mounts `&Handler{}` (nil root — would panic on getattr) and only `Stat`s the mountpoint dir. Never issues FUSE `Getattr`/`Lookup`/`Readdir`. `t.Skip` on darwin (no `/dev/fuse`) means macOS runs are green without exercising anything. Docs still cite it as "live mount" evidence.
- **`TestSkeletonSmoke`** (`internal/fusefs/fusefs_test.go:7`). Asserts `FsName=="syncfs"` and `AllowOther==false`. Never touches an inode. The handler is `&Handler{}` — root is nil.
- **`TestSessionExpiryDropsLocks`** (`internal/metadata/locks/locks_test.go:32`). `_ = m.Try(b)` (line 37) discards the error. Under the current family-ignoring logic `Try(b)` *fails* (flock 0-0 vs OFD 500-600 overlap as EOF-relative). The test then asserts `sessC` succeeds, which only works because `b` was never stored. Green *because* the bug fired and was thrown away. Same pattern `_ =` appears for the "concurrent change must be refused" path etc.
- **`TestIdentityRequiresExactlyOneURISAN`** (`internal/auth/auth_test.go:59`). Comment says "a leaf with two URI SANs must be rejected" — code creates `&x509.Certificate{URIs:nil}` and asserts zero-URI rejection. No two-URI cert is ever constructed. The `ErrMultipleURISAN` branch (`identity.go:47-49`) is untested. (Also has `_ = cert`, `_ = ca` dead lines.)
- **`TestRedact`** (`internal/auth/auth_test.go:101`). The `else if want := "[REDACTED]"; got != ... { _ = want }` branch is a no-op — any non-redacted, non-full-replacement output passes. Assert is vacuous for the interesting case.
- **`TestApplyAndClear`** (`internal/testkit/fault/fault_test.go:8`). Only exercises the bookkeeping struct. `Harness.Partitioned` returns true for *any* pair touching `NodeID` — the test never models a real edge (1↔2 vs 2↔3) or wires faults into `Mesh`. The harness doesn't inject faults into anything; `IsRealizable` always returns true except unknown kind.
- **`TestSweepEpochsAndReachability`** (`internal/chunkstore/gc/gc_test.go:15`). Sweeps a `Collector` over an **in-memory map** — never deletes anything from `chunkstore.Store` (there is no `Delete` method to call). M4-01 "epoch-based chunk GC" exists only as a list filter.
- **`BenchmarkWorkloadQuorumWrite`** (`test/benchmark/workload_test.go`). Uses **the same payload** every iteration. `Put` dedup (chunkstore.go:172) makes iterations 2..1000 a `Pebble.Get` fast path, not fsync. Reported 21,865 ops/s is dedup throughput, not quorum-write throughput. Report in `docs/benchmarks/README.md:23-25` is honest about *environment* (in-process, no QUIC, no fsync-honoring hardware) but not about the dedup artifact inflating the number ~100×. (`RenreadsPct` is also misspelled in `WorkloadMetrics`.)
- **`TestReplacementFlow`** (`internal/admin/member/member_test.go:9`). Asserts `Pending()==false` immediately after `RequestPromotion` — i.e., clearing pending on *emit* of the ConfChange, not after Raft commit. That *is* the Inv.8 bug, and the test enshrines it as expected behavior.
- **`TestXattrUserNamespaceOnly`** (checked-in) passes against the in-memory handler only; the *metadata* xattr path (`doSetXattr`) is a no-op and untested. The compat matrix says xattrs DEFERRED while `status.md` lists `Xattr user.* surface DONE` — both can't be fully true.

**Untracked evidence quality:** The `attacker_evidence_test.go` helpers (not in `git ls-files`) are actually *good* tests — specific, minimal, deterministic, and they fail for the right reasons. The tragedy is they aren't checked in. Recommendation: adopt them (with the bad `errors.Is(err, err)` tautologies in `chunkstore/attacker_evidence_test.go:91` and `transport/attacker_evidence_test.go:159` fixed) into the checked-in suite, or at minimum file them as regression tests before fixing.

---

## 4. Design inconsistencies

| # | Inconsistency | Where | Notes |
|---|---|---|---|
| D1 | `go.mod` requires `go 1.26.0` but host toolchain is `1.15.6`; `go vet/test` locally fails; tests only runnable via Docker image `golang:1.26-alpine` | `go.mod:3`, `Dockerfile`, `Makefile fleet-test` | Not a bug *if* fleet is the only gate, but CI should `docker run` explicitly; PROGRESS.md commands (`go test`) imply host Go works when it doesn't. |
| D2 | All deps in `go.mod` marked `// indirect` though `pebble`, `blake3`, `go-fuse`, `quic-go`, `raft` are directly imported | `go.mod:7-41` | `go mod tidy` stale. Misleads reviewers about what's direct. |
| D3 | Modules pinned (`pebble v1.1.5`, `quic-go v0.65.0`, `go-fuse v2.12.1`, `etcd/raft/v3 v3.5.35`) but spec §6 says "FIPS mode from v0.60" — no FIPS config anywhere; `blake3` via `lukechampine.com/blake3` (pure Go/x-arch, not RFC-verified EverLark? DECISIONS.md:5-11 justifies via "browser-testably" — thin rationale for a durability-critical digest) | `go.mod`, `DECISIONS.md` | Spec intent (FIPS) silently dropped. |
| D4 | Three different "quorum" models: `volume.Coordinator` (local map), `driver` raft (in-memory mesh), `testkit/cluster.Mesh` (ticks-everywhere) | `volume.go:18-32`, `driver_test.go:12-16`, `cluster.go:15-18` | PROGRESS.md physically cannot reconcile which mesh was "the 3-node mesh" — they differ. |
| D5 | `MountOpts()` vs `KernelMount()` use different `Options` objects; only one is tested | `fusefs.go:89-103` | Tested opts ≠ mounted opts. |
| D6 | `proto Marshal` size cap 1 MiB (`metadata.go:173`) vs framing `MaxManifestFrame 1 MiB` — consistent only by coincidence; `MaxControlFrame 64 KiB` ignored by `WriteFrame` (framing.go:48-73) → control bypass (evidence: `TestAttackerControlCapBypass` fails, 100 KiB Hello accepted) | `framing.go:48-73` | Frame class limits unenforced. Zero-length frame accepted (evidence: `TestAttackerZeroLengthFrame` fails). |
| D7 | `internal/testkit/pki` + `internal/testkit/cluster` have no tests; `test/fault/` empty dir | repo layout | Test infra claimed (M2-04/M4-04) but leaves no checked-in coverage. |
| D8 | `internal/observability` re-exports Prometheus via hand-rolled `Prometheus()` string (no `client_golang` despite dep in go.mod), duplicate metric name (`MetricAmbiguousOps` and `MetricAmbiguousOpsTotal` both = `"syncfs_ambiguous_operations_total"`, lines 57,63), `SetFromStats` drops `FollowerLag`/`ChunkChecksumFailures` (Stats fields 24,29 with no export) | `observability.go:20-34,50-64,97-109` | Hand-rolled exposition missing HELP/END markers; gauges for counters. |
| D9 | `docs/spec/status.md:7-28` says every milestone DONE with a one-line `go test` evidence string; `CHANGELOG.md:10-20` lists M3/M4 deliverables as added; `docs/reference/cli.md:3-10` says CLI is *stubs*. Both "M4-03 CLI + systemd: DONE" and "prints stub" can't be true in the same release. | status vs cli.md | Dishonesty/mislabel. Same for M2-04 "raft driver" (chunk gating stub) and M4-04 "fault harness" (log, no mesh wiring). |
| D10 | `PROGRESS.md:49-51` claims "clean-tree gate passed (12 packages)" — actual repo has 20 packages (count includes `api/contract`, `test/benchmark`, `cache`, `gc`, `lease`, etc.) | PROGRESS.md | Stale count; docs didn't track later additions. |
| D11 | `test/benchmark/workload_test.go` imports `t.Context()` (Go 1.24+ `testing.T.Context()`) — forces Go 1.24+ everywhere, yet host/CI docs don't pin that. `BenchmarkWorkloadQuorumWrite` mutates `b.N=1000` (unusual) and uses insertion sort, ignoring `slices`. | benchmark | Minor; shows Go version floor is load-bearing. |
| D12 | `internal/fusefs/posix.go` `Symlink` does `h.root.children[name].link = target` **without the inode's `mu`** (152-154) racing with concurrent readers — data race that `go test -race` doesn't trigger because tests are single-threaded. | `posix.go:149-156` | Real race on symlink read/write. |

---

## 5. Dishonesty register (claims the code does not back)

| Claim (file:line) | Reality | Severity |
|---|---|---|
| `PROGRESS.md:17` "M3-01 FUSE mount … PASS (completed)" | `KernelMount` mounts but `Lookup` returns nil inode; `Root.Getattr` panics on `&Handler{}`; `Open` ignores flags. Mounting ≠ working. | **Major** |
| `docs/spec/status.md:12-23` all M1-M4 items `DONE` | M2-05 proposal line is hardcoded to 1; M4-01 GC never deletes; M4-03 CLI is stub; M4-04 harness never injects. Each at best PARTIAL. | **Major** |
| `internal/client/client.go:24` "bounded, durable client operation journal" | In-memory map; lost on restart; TTL unenforced. | **Major** |
| `internal/raftstore/driver/driver.go:95-101` "Campaign gates on chunk presence" | `checkChunkGaps` is `return nil`. | **Major** |
| `internal/fusefs/posix.go:78-80` "mount stays fail-closed by refusing writes that return an error" | `_ = h.syncer.OnWrite(...)` discards the error and returns success. | **Critical** |
| `internal/fusefs/xattr.go:11` "Syncer is the coordinator seam M5 wires" named `NoopSyncer` acknowledges all writes | Coordinator seam acknowledges everything unconditionally until M5; currently FUSE writes are *not* coordinator-backed at all. | Major |
| `docs/benchmarks/results.json` 21,865 ops/s p50 0.021 ms | Dedup-iteration artifact; describes Pebble `Get`, not quorum fsync path. README admits different path but not the dedup inflation. | Minor |
| `docs/posix-compat.md:9-23` "+" | Matrix is the most honest doc in the repo (PARTIALs noted). But `status.md` flattens it to DONE. Keep matrix; demote status rows. | Minor |
| `internal/chunkstore/chunkstore.go:1-7` "torn trailing pack record is dropped on reopen" | Torn *tail* dropped; middle corruption truncates all later records; dangling index not cleaned. | Major |
| `internal/volume/volume.go:59-61` "Raft-ordered proposal lands in M2-06" **and** `status.md` "M2-05 DONE" + "M2-06 DONE" | Raft proposal wiring is *missing*, not "lands later". status.md should say PARTIAL for M2-05/M2-06. | Major |

`docs/known-limitations.md:6` "| 6 | persisted metadata index beyond Raft | in-memory scaffold | coordinator lands with M5 |" is honest about the gaps — the status table contradicts it.

---

## 6. How to make `go test ./... -race` meaningful

1. Adopt the untracked attacker tests (fix the two `errors.Is(err, err)` tautologies in `chunkstore/attacker_evidence_test.go:91` and `transport/attacker_evidence_test.go:159`), file them as regression tests, then fix code to make them green. The suite currently passes *because* it lacks these negatives.
2. Add missing negatives for every DONE row in `docs/spec/status.md`: snapshot-restore, compaction persistence, 2/3 commit, cross-replica deterministic replay, role enforcement on QUIC streams, control-frame cap, zero-frame rejection, payload-before-reference (dangling manifest rejected), journal persistence, FUSE `Lookup`/`ls` via kernel mount, CLI daemon `ExecStart` actual run (not stub).
3. Replace `_ =` discards in checked-in tests with error assertions (`locks_test.go:36-37`, `client_test.go` cache) and replace the `TestWriteChunksMinorityRefused` dead branch with a proper `errors.Is(err, ErrNoQuorum)` check (requires wiring the sentinel, #10).
4. Add `-race` pressure: concurrent `Put`/`Get` (already present as untracked stress), concurrent `Consume` (TOCTOU), concurrent `BlockingGrant` (SIGSEGV), concurrent `WriteFrame` (stream interleave). None run today.
5. Benchmark: use fresh payload per iteration (UUID + counter) or `Put` to distinct prepopulated stores; otherwise re-baseline. Report ops on fsync path honestly.

---

## Appendix A — Commands run

```
docker run --rm -v /Users/mohamed/syncfs:/src -w /src golang:1.26 go test ./... -race -count=1
  → all 20 packages OK (checked-in suite only)

docker run --rm -v /Users/mohamed/syncfs:/src -w /src golang:1.26 go test ./internal/... -count=1
  → 30+ failures incl. panics in:
    wal: TestAttackerSnapshotNotRestoredOnReopen, TestAttackerGapWritePoisonsDurableLog,
         TestAttackerCompactUndoneByReopen, TestAttackerMiddleCorruptionSilentlyTruncatesCommittedEntries
    chunkstore: TestAttackerMiddleCorruptionDropsLaterDurableChunk,
                TestAttackerPutAfterCorruptionClaimsSuccessWithoutHealing
    fusefs: TestAttackerRenameFileOntoDir, TestAttackerNamesWithSlashAndDots,
            TestAttackerBareUserXattr, TestAttackerWriteOverflowPanics, TestAttackerXattrDoesNotBumpMtime
    metadata: TestAttackerLinkToMissingParentPanics (panic), TestAttackerRenameToMissingDstParentPanics (panic),
              TestAttackerDotNamesAccepted, TestAttackerRenameAcceptsSlashInDstName,
              TestAttackerUnlinkLeavesZombieInode
    locks: TestAttackerCrossFamilyFalseConflict, TestAttackerBindProtoDropsInodeAndRange,
           TestAttackerBlockingGrantPanicsOnSecondLock (SIGSEGV at locks.go:130),
           TestAttackerBlockingGrantStarvedByReleaseSteal, TestAttackerSameOwnerOverlappingUpgradeBlocked
    volume: TestAttackerNoQuorumErrorIsSentinel, TestAttackerCancelledContextStillWrites,
            TestAttackerProposeRaftIndexesAreIdentical, TestAttackerPartialChunkBatchLeavesOrphan
    transport: TestAttackerControlCapBypass, TestAttackerZeroLengthFrame,
               TestAttackerTLSNoClientCertRejected, TestAttackerTSDowngradeRejected (tail)
    auth: TestAttackerEnrollmentConcurrentDoubleSpend (2 successes),
          TestAttackerEnrollmentEmptyProofAccepted
```

## Appendix B — Fix priority (smallest critical path first)

1. Locks panic + family (10 lines) and metadata nil-map panics (guard checks) — both are one-guard crashes.
2. Checkpoint restore + WAL validate-before-write + middle-corruption fail-closed.
3. Deterministic IDs + `doWrite` receipt check + unlink link-count.
4. `WriteFile` error propagation + offset cap + `Lookup` inode.
5. Coordinator: real Raft index, `ErrNoQuorum` wrap, `ctx` checks, per-peer receipts.
6. Enrollment TOCTOU + `VerifyConnection` + role enforcement.
7. FUSE path resolution + name validation + cache opts + lock forwarding.
8. Client journal durability + reconnect lookup + lease-generation checks.
9. Membership pending semantics + GC deletion wiring + CLI/harness honesty (demote status rows to PARTIAL until wired).

All fixes above are scoped to single files/packages and verifiable with the attacker tests in Appendix A plus the additions in §6. No new Go modules are required.

---

## Round 2 — 2026-11-08 — Tandem re-verification (baseline `0fef618` + uncommitted partial fixes)

> Baseline: `0fef618f7897b12e39b32557d99240d8ac1f12a2` (“Benchmark workload harness”). Working tree at audit: 3 modified files (uncommitted) + 2 untracked reports (`research/01-critic-findings.md`, `research/02-tester-evidence.md`). Verified with Go `1.26.0` (`/private/var/folders/n_/_hxsf0_n5rsd_3bg3vlh122h0000gn/T/opencode/go1.26/bin/go`, `GOTOOLCHAIN=local`; host `go1.15.6` cannot build this repo). Read-only audit; no production code edited.

### Round 2A. Delta since Round 1 — what actually landed

| File | Change | Effect |
|---|---|---|
| `internal/metadata/locks/locks.go` | `conflicting` adds `Family` + same-owner `return false` (`locks.go:66-81`); new `purgeSameOwnerOverlapLocked`/`overlap` (`102-128`) + call sites in `Try:97`/`tryLocked:162`; deleted `usableLocked` + `sort.Slice(nil)`; `Release:169-186` no longer dequeues head (closes `ready` with double-close guard, head dequeues itself in `BlockingGrant:147`); `BindProto:205-230` now copies `InodeId`/`RangeStart`/`End` and handles `LOCK_RELEASE` | Fixes 5 of 5 locks sub-findings at the API level (see §Round 2C). Second partial fix beyond the “one fix” described — `xattr.go` also changed (below). |
| `internal/metadata/locks/locks_test.go` | +4 tests: `TestFamilySeparationIsTotal`, `TestSameOwnerNeverSelfBlocks`, `TestBlockingGrantAcquiresAfterRelease`, `TestBindProtoCarriesInodeAndRange` (all now checked into working tree, import `time` + `syncfsapi`) | First checked-in regression tests for locks. Note: file flapped mid-audit (unused `time` → unused `syncfsapi` → green) due to a concurrent editor; final snapshot green. See §B. |
| `internal/fusefs/xattr.go` | `SetXattr:57` `ns.mtime = ns.mtime` → `ns.mtime = time.Now()`; dropped `var _ = os.Stdout` | Fixes 1 of 5 fusefs tester findings (`TestAttackerXattrDoesNotBumpMtime` + `go vet` self-assignment). Nothing else in FUSE changed. |

Everything else is byte-identical to `0fef618`. In particular WAL, metadata, chunkstore, volume, driver, auth, lease, client, transport, FUSE Lookup/WriteFile, benchmark are untouched.

### Round 2B. Test / vet transcript — per-package (Go 1.26, `-race -count=1`)

Final stable snapshot (after concurrent locks_test fix landed):

```
go test ./... -race -count=1  → EXIT 0
ok  api/v1/contract, cmd/syncfs, internal/admin/member, internal/auth,
    internal/chunkstore, internal/chunkstore/gc, internal/client,
    internal/client/cache, internal/fusefs, internal/metadata,
    internal/metadata/locks, internal/observability, internal/raftstore/driver,
    internal/raftstore/snapshot, internal/raftstore/wal, internal/testkit/fault,
    internal/transport, internal/volume, internal/volume/lease, test/benchmark
?   api/v1/gen, internal/testkit/cluster, internal/testkit/pki [no test files]
→ 20 ok + 3 no-test = 23 packages total (go list ./... = 23)

go vet ./... → EXIT 0 (final)
```

Caveat (harsh): “20 ok” does **not** mean the invariants hold. It means the *checked-in* suite (which lacks every attacker negative from `02-tester-evidence.md`) passes. Two transient reds were observed mid-audit in `internal/metadata/locks` (unused `time`, then unused `syncfsapi` build failures) because a second agent edited `locks_test.go` concurrently. Any gate that cites “all green” without pinning a commit hash is quoting a moving tree. The Round 1 warning stands: green is over-claimed, and `docs/spec/status.md` still claims every M1–M4 item `DONE` (see §F).

### Round 2C. Re-verification verdicts — CRITICAL 1–9 / MAJOR 10–18

`HOLDS` = bug still present at current tree · `PARTIAL` = API fixed but wiring/residue remains · `FIXED` = closed.

| # | Verdict | Evidence (current `file:line`) |
|---|---|---|
| CRITICAL 1 — WAL snapshot never restored | **HOLDS** | `wal/wal.go:91-102` `Open` replays only `wal.log`; never `snapshot.ReadSnapshotFile`. `ReadSnapshotFile` (`snapshot/snap.go:65`) zero production callers (only `snap_test.go`). Restart loses `ConfState`/commit → Inv.9, §8.4/§8.5. |
| CRITICAL 2 — WAL `Compact` memory-only / index math wrong | **HOLDS** | `wal/wal.go:348-363` drops `s.ents` but never advances `s.snapshot`, never rewrites `wal.log`; `Entries:283`/`Term:309`/`FirstIndex:332` still compute from `snapshot.Index (0)`. Reopen resurrects. Inv.9, §8.4. |
| CRITICAL 3 — WAL gap poisons durable log + middle-corruption silently drops committed entries | **HOLDS** | `SaveReady:207-217` `writeRecord` (212) *before* `appendEntryLocked` validation (215) → rejected gap fsynced; `replay:104-129` any `decodeRecord` error → `truncate + succeed` (122-128), no tail-vs-middle distinction; `TruncatedErr` unread. Inv.9, §8.4. |
| CRITICAL 4 — `doCreate` `crypto/rand` IDs → replica divergence | **HOLDS** | `metadata/metadata.go:267-268` `rand.Read(newID[:])`. Same command → different IDs per replica; breaks “replay is pure” (header `:4`). Inv.2, §8.2/§8.4. |
| CRITICAL 5 — `doLink`/`doRename` nil-map panics on missing parent | **HOLDS** | `metadata.go:335` `m.dentries[parent][p.Name]=target` (no parent check); `metadata.go:374` same for `dstPar`. `doCreate:258` guards, `doLink`/`doRename` don’t. Follower-apply panic = cluster brick. Inv.9, §7.4. |
| CRITICAL 6 — locks: family ignored, segfault, `BindProto` drops inode/range, Release steal, self-block | **PARTIAL → MAJOR residue** (see §D) | Family/segfault/BindProto/steal/self-block API now fixed (`locks.go:66-81,102-128,147,169-186,205-230`) + 4 new tests. Residual: `BlockingGrant` unbounded/no-`ctx`, `ApplyReleases` no wake, queue keying, idempotence-ignores-Family, purge exact-range leak, `metadata.go:235` no-op wiring. Downgraded from CRITICAL to MAJOR. |
| CRITICAL 7 — chunkstore middle-corruption drops later chunks; dedup lies; unchecked index | **HOLDS** | `chunkstore.go:127-151` `scanPack` truncates on first `readRecord` error (139-143); `Put:172-175` index-hit receipt without read-back BLAKE3; `lookupLocked:282-283` `v[:4]/v[4:]` no length check; `Get:233` `packs[loc.packNumber]` no nil check (sparse `nil` pads). Inv.9, §7.5, §8.2 step 4. |
| CRITICAL 8 — auth TOCTOU double-spend + empty proof + `VerifyPeerCertificate` lie + role unenforced | **HOLDS** (all 3) | `auth/enrollment.go:77-91` `Get`→validate→`MarkConsumed:91` non-atomic; `MarkConsumed:57-67` unconditional overwrite; `Put:40-48` no empty-fingerprint/nonce validation (`ConstantTimeCompare("","")==1`). `identity.go:76` chain+cluster only, never `RoleFor` (`types.go:35` test-only); `transport/session.go:33,51` registers `VerifyPeerCertificate` contradicting `identity.go:74-75` MUST `VerifyConnection` + spec §8.1. §8.1, §9/§12.1, Inv.9. |
| CRITICAL 9 — FUSE `WriteFile` swallows error + overflow panic + `Lookup` nil inode | **HOLDS** | `fusefs/posix.go:83` `_ = h.syncer.OnWrite(...)` then `return success:84` despite “fail-closed” comment (78-80); `end:=at+len:72-74` no cap/overflow → `makeslice/growslice` panic; `fusefs.go:160` `Lookup` `return nil, 0` (nil `*fs.Inode`); `Open:164-168` ignores flags, always `DIRECT_IO`; `Getattr:144-145` nil-`root` on `&Handler{}`. Inv.10, §8.3. |
| MAJOR 10 — coordinator local-only, hardcoded `RaftIndex=1`, no quorum sentinel / `ctx` / receipts | **HOLDS** | `volume/volume.go:68` `idx:=uint64(1)`; `WriteChunks:37`/`ProposeCommand:62` never read `ctx`; `:52` bare `fmt.Errorf` (never `%w ErrNoQuorum:77`), message hardcodes `chunk 0`; `chunkstore.Receipt:40-44` no `PeerID`; `WriteChunks`/`ProposeCommand` disconnected; orphans unrecorded. Inv.3, Inv.5, §8.2 steps 5-8. |
| MAJOR 11 — driver chunk-gap stub, `voters` ignored, apply blocks Ready | **HOLDS** | `driver/driver.go:103-106` `checkChunkGaps(){return nil}`; `chunkPresent` unused; `Campaign:96-100` always proceeds; `:43` `_ = voters`; `Step:76` `d.applyRequested <- e` sync-blocks heartbeat loop. Inv.4, §8.4. |
| MAJOR 12 — metadata dangling manifests, zombie inodes, dot/slash names, no-op xattrs, `uid/gid=Role` | **HOLDS** | `metadata.go:274-293` `doWrite` never checks `PreparedChunks`; `doUnlink:340-356` no link-count/tombstone; `validName:404-409` accepts `.`/`..`; `doRename:358-378` no `validName` on src/dst (slash accepted); `doSetXattr:395-397 return nil`; `doVolumeConfig:399-402 quota++` ignores payload; `:269 UID/GID=cmd.Role`; `doTruncate:307` size/manifest diverge; only flat root files (`KindDir` create unreachable). Inv.3/Inv.4, §7.4. |
| MAJOR 13 — client journal in-memory, TTL dead, `Reconnect` ignores lookup, cache keeps leases | **HOLDS** | `client/client.go:26-31` `Journal` map, no persistence; `pruneLocked:62-72` one *random* victim (map iteration), `at` never checked in `Lookup:52-60`; `Reconnect:103-112` checks `lookup==nil` but never calls `lookup`; `cache/cache.go:58-62` `InvalidateAll` clears entries, not `leases`. Inv.7, §8.2, Inv.6. |
| MAJOR 14 — `ctx`/deadline ignored; `BlockingGrant` unbounded, no `EDEADLK` | **HOLDS** | `volume.go:37,62` `ctx` unused (no `select ctx.Done`); `locks.go:132-153` `BlockingGrant(l Lock)` no `ctx`, infinite `1 ms` ticker loop, no timeout/deadlock detection despite comment “or the ctx expires”. §8.2 5-s `EIO` deadline, §8.3 `EDEADLK`. |
| MAJOR 15 — FUSE flat, cache-unsafe, lock-free, name/rename/xattr lax | **HOLDS** (mtime fixed) | `posix.go:14-27` root-only, no path resolution; `fusefs.go:97-103` mounted `Options` ≠ tested `MountOpts:89-94`, no TTL/`DIRECT_IO`/`Forget` policy; no lock forwarding; `Rename:136-146` blind overwrite (file→dir OK); `createNode` accepts `a/b, ., .., "", NUL`; `xattrName` accepts bare `user.`; **`SetXattr` mtime now FIXED** (`xattr.go:57 time.Now()`). Inv.6, Inv.10, §8.3. |
| MAJOR 16 — lease generation never checked | **HOLDS** | `lease/lease.go:86-94` `Valid` checks `Revoked/Acked/ExpiresAt` only, never `Generation` vs current; `Grant:45-58` trusts caller generation; `metadata.go` bumps `generations` only on unlink/rename (`354,375`). §8.3. |
| MAJOR 17 — `OpResult` unbounded, no payload binding on op-ID reuse | **HOLDS** | `metadata.go:70,206` `ops` unbounded (spec N=10,000/10 min §8.2); duplicate check `:178-180` returns `ErrCommandFailed` without comparing payloads → reused op-ID with different payload returns old result. Inv.7. |
| MAJOR 18 — WAL file creation not dir-synced | **HOLDS** | `wal.go:92` `O_CREATE` on `wal.log` with no parent-dir fsync (snapshot `snap.go:56-60` does it for rename; chunkstore `Put:195` does `syncDir`). §7.5. |
| Benchmark dedup inflation | **HOLDS** | `test/benchmark/workload_test.go:94,102` same `payload` every iter → `chunkstore.Put` dedup fast path (`chunkstore.go:172`); `b.N=1000:98` forced; reported `ops/s` is Pebble `Get`, not quorum fsync path. §11 accounting dishonesty. |
| `docs/spec/status.md` DONE register | **HOLDS (dishonest)** | `status.md:12-28` still all `DONE` incl. M2-05 (idx=1), M1-03 (TOCTOU), M2-06 (locks residue), M4-01 (GC never deletes), M4-03 (CLI stub), M4-04 (fault harness no wiring). Demote to `PARTIAL` until §E fixes land. |

### Round 2D. Locks partial-fix forensics — fixed vs still broken

**Actually fixed (verified, now with checked-in tests):**
1. **Family separation** — `conflicting` now `if a.InodeID != b.InodeID || a.Family != b.Family return false` (`locks.go:67`). `TestFamilySeparationIsTotal` green. Spec §8.3 G-1 honoured at `Try`/`tryLocked` level.
2. **`sort.Slice(nil)` segfault** — `usableLocked` deleted, call removed (`BlockingGrant:147` no longer calls it). `TestBlockingGrantAcquiresAfterRelease` green (2 locks no longer panic).
3. **Release steal / starvation** — `Release:180-186` no longer dequeues head; closes `ready` (double-close guarded) and leaves dequeue to the acquiring waiter (`m.queues[inode]=m.queues[inode][1:]` at `147`). Starvation repro from `02-tester-evidence.md §2.5` would now wake B on next tick.
4. **`BindProto` inode/range drop** — `BindProto:208-210` now `InodeID: ino, Family, OwnerKey, Start: p.RangeStart, End: p.RangeEnd, Mode`. `TestBindProtoCarriesInodeAndRange` green (different-inode grant succeeds).
5. **Same-owner self-block (liveness)** — `conflicting:75-77` `if a.Owner==b.Owner return false` + `purgeSameOwnerOverlapLocked` (`102-118`). `TestSameOwnerNeverSelfBlocks` green (shared→exclusive upgrade no longer `WouldBlock`).
6. **`LOCK_RELEASE`** — `BindProto:212-228` now has a release path (full-width per inode). Test covers grant→release→re-grant.

**Still broken / newly introduced residue:**
1. **`BlockingGrant` still unbounded, no `ctx`, no `EDEADLK`** — signature `BlockingGrant(l Lock) error` (`132`) has no `ctx`; loop `for { <-ticker.C ... }` (`140-153`) is infinite. Comment “or the ctx expires” lies. Client timeout leaks goroutine; deadlocked waiters spin forever. MAJOR 14 HOLDS.
2. **`ApplyReleases:190-201` still doesn’t wake waiters** — clears `byID` but closes zero `q[*].ready` channels. Correctness papered over by 1-ms ticker poll, but not event-driven; wasted wake latency + no `ready` contract.
3. **`ready` channel is now decorative** — `Release` closes `q[0].ready` but `BlockingGrant` never `select`s on `w.ready` (only `ticker.C:141`). The close is a no-op for liveness; the old “steal” is fixed only because dequeue moved, not because wake-up works.
4. **Queues keyed by `InodeID` only** (`queues map[[16]byte]`) — flock head blocks fcntl/OFD waiters on same inode even though `Try` allows cross-family coexistence. Head-of-line blocking across families; `tryLocked` would succeed but never gets to try. Should be `(Inode, Family)` or offer-scan.
5. **`Try` idempotence ignores `Family`** — `Try:89` compares `Owner/Start/End/Mode` only. Same owner+range+mode but different family (flock then fcntl) false-hits idempotent `return nil` without recording second lock. Must add `Family`.
6. **Purge misses exact-range upgrade** — `purgeSameOwnerOverlapLocked:108-109` skips purge when `h.Start==l.Start && h.End==l.End`. Exact-range Shared→Exclusive (the test’s case!) leaves **both** entries (old shared + new exclusive) instead of superseding. Test only asserts “no error”, not that old lock is gone. `Release` (exact mode match) then removes only one.
7. **`LOCK_RELEASE` ignores `Family/Range/Mode`** — releases *all* families/ranges for owner on inode (`216-224`). Coarse but safe-ish; document or narrow.
8. **`BindProto` still not wired into state machine** — `metadata.go:235-237` `LOCK_GRANT/LOCK_RELEASE → return nil` no-op; `BindProto` is dead code except for its own test. No end-to-end Raft-ordered lock path. Fix requires calling `BindProto` from `applyKind` *or* deleting the no-op lie.
9. **`idx` param ignored** — `BindProto(cmd, idx)` never uses `idx`; no Raft-order/lease interaction.

Net: CRITICAL 6 is downgraded to MAJOR. The fix is a genuine improvement (4/5 API bugs closed + tests), but the “BlockingGrant starved / BindProto drops / unbounded ticker / no wake” residues remain load-bearing for §8.3.

### Round 2E. Prioritized fix list — smallest critical path first (invariant-mapped)

Each item lists spec invariant(s) so a PR can be gated.

| Pri | Fix (smallest change, ordered) | Files / lines | Invariants · Spec |
|---|---|---|---|
| P0-1 | Deterministic inode IDs + missing-parent guards + link-count/zombie + `validName` (`"."`, `".."`, rename src/dst+link validate) + `0377` `doWrite` manifest-length already there — add `PreparedChunks`/payload-presence reject | `metadata.go:267-269,335,374,404-409,340-356,274-293` | **Inv.2** (total order) · **Inv.9** (fail closed, no panic-brick) · §7.4 atomic · §8.2 step 7 |
| P0-2 | WAL: `Open` restores snapshot (`ReadSnapshotFile` → seed `s.snapshot`, skip `ents ≤ snap.Index`, `Commit/Term = max`), validate-before-write + truncate-on-error, tail-vs-middle corruption (middle → hard `Open` error + quarantine), durable `Compact` (require `compactIndex==snapshot.Index` or persist base + rewrite log), dir-sync on create | `wal/wal.go:91-102,104-131,152-163,207-217,348-363` + `snap.go:23-62` | **Inv.9** · §8.4 (persist-before-send, in-order) · §8.5 (envelope activation) · §7.5 dir-sync |
| P0-3 | Chunk integrity: `scanPack` tail-only truncate (middle → loud error, rebuild index from validated records), `Put` verify-on-dedup (BLAKE3 read-back or `ErrCorrupt`+heal), `lookupLocked` bounds-check, `Get` nil-pack check, squash dangling `Has` | `chunkstore/chunkstore.go:127-151,172-176,275-285,220-241` | **Inv.9** · §8.2 step 4 (verify+force durable) · §7.5 pack `[len][BLAKE3][data][fsync]` |
| P0-4 | FUSE fail-closed + bounds + real lookup: propagate `OnWrite` error (`posix.go:83`), cap `at+len` → `EFBIG/EINVAL` before alloc (same for `Truncate`), `Lookup` return valid `*fs.Inode` (or `NodeLookup`), `Getattr` nil-root guard, `Open` honour flags | `fusefs/posix.go:57-85`, `fusefs/fusefs.go:144-168` | **Inv.10** (no hidden downgrade) · **Inv.6** · §8.3 |
| P0-5 | Coordinator Raft+quorum: real `RaftIndex` from driver commit (kill `idx:=1`), link `WriteChunks` receipts → `ProposeCommand` (receipts in command, `doWrite` checks), `ErrNoQuorum` wrap (`%w`) + real `i`, `ctx` checks entry+per-chunk, per-peer receipts (`PeerID` in `Receipt`), lease revocation (`Revoke+WaitClear`) before propose | `volume/volume.go:18-77`, `chunkstore:38-44`, `metadata.go:274-293` | **Inv.3** (payload-before-ref) · **Inv.5** (no quorum) · §8.2 steps 5-8 |
| P0-6 | Driver gap gating: implement `checkChunkGaps` (committed manifests vs `chunkPresent`), `Campaign` refuses with gap, `CATCHING_UP` cannot campaign/vote (promoted-but-unapplied voter *does* vote — G-2 exception), non-blocking buffered apply (no sync `Step←apply` stall), assert ConfState non-empty | `raftstore/driver/driver.go:43,62-83,95-106` | **Inv.4** (apply-only-with-payload) · §8.4 rules 3-4 |
| P0-7 | Auth: atomic consume (CAS/`ConsumeLocked`/single-lock, `MarkConsumed` refuses re-consume), reject empty/oversize fingerprint+nonce in `Put`+`Consume`, `VerifyConnection` (not `VerifyPeerCertificate`) + per-stream role (raft⇒voter/learner node, chunk⇒node, client⇒client/admin) | `auth/enrollment.go:40-92`, `auth/identity.go:76`, `transport/session.go:23-54` | §8.1 (`VerifyConnection`, 1-URI SAN) · §9/§12.1 one-time · **Inv.9** |
| P0-8 | Leases/cache/journal/idempotency: `Valid` compares `Generation` (+ `currentGen` callback), bump generations on create/link, `InvalidateAll` clears leases, journal persisted (WAL) + TTL sweep + `Reconnect` actually calls `lookup` for unknowns, `OpResult` bound (10k/10 min) + `protoHash` payload binding (duplicate different-payload → `REJECTED`, never return old `COMMITTED`) | `lease.go:86-94,45-58`, `cache.go:58-62`, `client.go:26-112`, `metadata.go:70,120-180,206,340-378` | **Inv.6** · **Inv.7** · §8.2 retention/deadline · §8.3 lease gating |
| P0-9 | Locks residue + FUSE surface + benchmark honesty: `BlockingGrant(ctx)`, `ApplyReleases` wake, queues by `(Inode,Family)`, idempotence `+Family`, purge exact-range supersede, wire `BindProto` in `metadata.go:235` (or delete no-op), FUSE paths/names/`MountOpts` unification/lock forwarding, fresh-payload benchmark | `locks.go:85-230`, `metadata.go:235-237`, `posix.go:14-27,136-146`, `fusefs.go:89-103`, `workload_test.go:94-110` | §8.3 (3 families, `EDEADLK`, `DIRECT_IO`) · **Inv.10** · §11 accounting |

### Round 2F. MATURITY GATE — “ready to benchmark edge cases”

**Do not benchmark edge cases until every G-item below is green with checked-in (not untracked) tests. Edge-case numbers today measure the wrong system:**

- **G1 Determinism** — two clones replay same `CREATE` → same inode ID; `Put` dedup verified.
- **G2 WAL crash suite** — gap-write not persisted; middle-corruption → hard error/quarantine (not silent `LastIndex=0`); snapshot restore (`FirstIndex==snap.Index+1` after reopen); `Compact` durable.
- **G3 Chunk recovery** — middle-corruption preserves later durable chunk; `Put`-after-corruption heals or returns `ErrCorrupt`; short Pebble value/sparse pack → error, not panic.
- **G4 Coordinator** — distinct `RaftIndex` per proposal; `errors.Is(err,ErrNoQuorum)` true; cancelled `ctx` writes nothing; majority receipts required before `ProposeCommand` commits; `doWrite` rejects dangling manifest.
- **G5 Driver** — `Campaign` refuses with unapplied chunk gap; `CATCHING_UP` cannot campaign/vote (except promoted-voter exception test); `Step` never blocks on apply lag.
- **G6 Auth** — concurrent `Consume` succeeds exactly once; empty proof rejected; `VerifyConnection` enforces per-stream role (client cert on raft stream rejected; resumed-connection re-validation tested).
- **G7 FUSE** — `WriteFile` propagates syncer error (fail-closed test with failing `Syncer`); overflow → `EFBIG/EINVAL` (no panic fuzz); kernel `ls/stat` via `Lookup` returns valid inode; `Open` honours flags.
- **G8 Locks end-to-end** — family isolation, `BindProto` via `metadata.Apply` (not just direct `BindProto`), `BlockingGrant(ctx)` cancellable, `EDEADLK` cycle test, `ApplyReleases` wakes waiters.
- **G9 Leases/journal/op-IDs** — stale-generation lease `Valid==false`; `InvalidateAll` clears leases; journal survives restart + TTL evicts; reused op-ID/different-payload → `REJECTED`, not old `COMMITTED`.
- **G10 Honesty + harness** — `status.md` demoted to `PARTIAL` for all unwired milestones; attacker tests from `02-tester-evidence.md` adopted as checked-in regressions (with the two `errors.Is(err,err)` tautologies fixed); benchmark uses fresh payload per iteration (UUID+counter) and reports fsync-path ops honestly; `go test ./... -race` + `go vet ./...` green on a pinned commit.

Until G1–G10 are green, throughput/latency numbers are dedup-cache hits, local map fan-out, unauthenticated, non-fsynced, non-Raft-ordered artifacts. Run the gate first; then benchmark edge cases.

### Round 2G. Dishonesty register — update (harsh)

`docs/spec/status.md` has not been touched since Round 1. Every `DONE` below still overclaims:

- **M2-05 “quorum writes DONE”** — `ProposeCommand` hardcodes `RaftIndex=1` (`volume.go:68`), no Raft, no receipts, no `ctx`. At best `PARTIAL`.
- **M1-03 “auth/PKI DONE”** — `Consume` double-spends, `VerifyConnection` promised but `VerifyPeerCertificate` shipped. At best `PARTIAL`.
- **M2-06 “leases/locks/cache DONE”** — locks API improved, but `BlockingGrant` unbounded, `Valid` ignores generation, `InvalidateAll` keeps leases, `metadata.go:235` lock no-op. At best `PARTIAL`.
- **M2-01/M2-04 “WAL/snapshot & raft driver DONE”** — snapshot write-only, `Compact` fake, `checkChunkGaps` stub. At best `PARTIAL`.
- **M4-01 “snapshots+GC DONE”** — `GC` sweeps an in-memory map, never deletes (`chunkstore/gc`). At best `PARTIAL`.
- **M4-03 “CLI/systemd DONE”** vs `docs/reference/cli.md:3-10` “stubs”; **M4-04 “fault harness DONE”** vs `Harness` that never injects into `Mesh`. Both `PARTIAL`.
- **`internal/fusefs/posix.go:78-80` “stays fail-closed”** while `_ = OnWrite` succeeds — Critical lie, unchanged.
- **`docs/benchmarks` 21,865 ops/s** — dedup artifact, still not disclaimed in README. Re-baseline with fresh payload after G4.

Keep `docs/posix-compat.md`’s honest `PARTIAL`s; flattening them to `DONE` in `status.md` is the documentation bug.

**Bottom line Round 2:** two partial fixes landed (locks API + xattr mtime), 8.5/9 CRITICAL and 9/9 MAJOR still hold. The locks fix is real but leaves MAJOR residue (unbounded wait, no wake, queue keying, dead wiring). Nothing about WAL/metadata/chunkstore/volume/driver/auth/FUSE correctness changed. The tree is benchmark-ineligible until the §F gate is green — edge-case benchmarks before then measure lies.
