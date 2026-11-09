# Known limitations

Honest register. Nothing on this list is silently dropped. Items 7–13 come
from the adversarial audit in `research/01-critic-findings.md` +
`research/02-tester-evidence.md` (2026-11-08); audit fixes are listed where
landed, with residual caveats where they remain.

| # | Limitation | Impact | Recovery / path |
| --- | --- | --- | --- |
| 1 | xattr / ACL namespaces | PARTIAL | `user.*` with non-empty suffix live (Handler: bare `user.`/NUL -> EINVAL, `security.selinux` -> EPERM, else EOPNOTSUPP; state machine rejects non-`user.*` as `ErrCommandFailed`); ACL policy enforcement pending |
| 2 | writable shared mmap | EXCLUDED, no handler yet | none (excluded by design; add an explicit FUSE `Mmap` handler returning `EOPNOTSUPP` — today the kernel gets the default ENOSYS) |
| 3 | O_DIRECT explicitness | `EINVAL` on Linux in strict mode (`oDirectFlag` build-tagged; 0 off-Linux) | use streaming reads |
| 4 | offline writes | unavailable | designed out (strict volume) |
| 5 | target-app qualification | deferred to M5 (ADR-0001) | needs reference hardware + chosen target app |
| 6 | persisted metadata index beyond Raft | in-memory scaffold | coordinator lands with M5; FUSE syncer seam implemented but production coordinator wiring pending; Pebble is the chunk index only |
| 7 | WAL snapshot + compaction | FIXED for restore/gap (snapshot restored on reopen; gap validated before write; mid-log corruption fails closed). `Compact` still refuses beyond snapshot (durable compaction pending). | manual: re-bootstrap node from verified snapshot; do not rely on `Compact` until durable |
| 8 | metadata determinism + parents | FIXED (deterministic SHA256 IDs from `(OpID, parent, name)`; parent checks in Link/Rename; Rename no-overwrite in the state machine; Truncate cap 32 MiB; bare-xattr reject; LinkCount zombie-free). Note: the Handler surface allows flat-root overwrite with EISDIR/ENOTDIR/ENOTEMPTY checks and caps at 16 MiB — the two surfaces diverge, see `docs/posix-compat.md` | — |
| 9 | chunkstore corruption + dedup | FIXED for mid-pack skip + dedup verify (+ bounds + nil-pack guards). `TestTornWriteRecovered`: torn tail truncated on reopen; first committed chunk survives; amputated-sync chunk is dropped or `ErrCorrupt`, never silently wrong (quarantine is skip, not forensic copy). | manual: scrub + repair from majority |
| 10 | enrollment + role enforcement | PARTIAL (empty-proof rejected, `MarkConsumed` guards double-consume; `VerifyConnection` wired (`VerifyConnectionFunc`) + per-stream `AllowedPrincipalForClass`/`AcceptClassifiedStream` enforced. `Consume` Get-then-Mark TOCTOU remains for generic stores; opener-side `ValidatePrincipalForClass` is caller duty — `OpenClassifiedStream` does not yet auto-check the opener role (QUIC `ConnectionState` exposes peer only). | manual: rotate enrollment nonces; openers must call `ValidatePrincipalForClass` before `OpenClassifiedStream` |
| 11 | volume coordinator | FIXED for sentinel/ctx/batch (wraps `ErrNoQuorum`; honors `ctx`; pre-validates batch; monotonic RaftIndex) | orphan sweep via GC remains the safety net |
| 12 | transport framing | FIXED for zero-length + per-class caps + single-writer mutex | — |
| 13 | benchmark qualification gap | numbers are in-process / Docker VM only (no QUIC/FUSE/10 GbE); §11 targets UNVERIFIED | run §11 gates on reference hardware — see `docs/benchmarks/README.md` |
| 14 | driver chunk-gap gating | PARTIAL (manager GREEN: explicit `Campaign` refusal + deferred apply + non-blocking `Step` + voter validation, `TestApplyDeferredForMissingChunk`/`TestCampaignRefusedWithChunkGap`; E2E wiring pending: auto-tick elections are raft-internal, callers must consult `ChunkGap` after election before serving writes, and the volume layer does not yet wire this check). | fetch+verify missing chunks, call `RetryApply`; review driver tests before production use |
| 15 | FUSE inode tree + open flags | PARTIAL (nil-handler fails closed (`TestNilHandlerFailsClosed`), `O_DIRECT` → `EINVAL` on Linux via `oDirectFlag` (build-tagged), strict default fail-closed (`NewStrictHandler` + `TestStrictHandlerFailsClosed` — writes return `EIO` with no coordinator; `NewHandler` is the test-only (Noop) surface). `Lookup` still returns nil inode with entry attrs; full go-fuse child tree (`NewPersistentInode`, `Readdir`, `Forget`) is pending — kernel use is stat-root only until then. | mount with a real root+child inode tree; test `ls` via `/dev/fuse` |
| 16 | blocking locks | FIXED (manager + state-machine: `BlockingGrantCtx` cancellable + `ErrDeadlock` (cross-inode BFS, chaos j1 PASS), family queues `(Inode,Family)`, `ApplyReleases`/`Release` wake, metadata `LOCK_GRANT`/`LOCK_RELEASE` now apply via `BindProto` (atomic clone, `Clone`); `BlockingGrant` remains as the unbounded wrapper for compat.) | callers should migrate to `BlockingGrantCtx` with a deadline |
| 17 | group commit + daemon wiring | spec §11 group commit (0–5 ms, leader-only batching) not implemented; coordinator is in-process fan-out; `daemon`/`mount` binaries are validate-and-print stubs | M5 work; until then the fleet test (`go test`) is the verification surface |
| 18 | op retry after eviction | retained results bounded (10,000 entries; server by Raft index, client journal 10 min). A retry past the window is forgotten and may re-execute (double-apply). Clients must surface `EIO` and reconcile with a new op-ID — see `docs/failure-semantics.md` | reconcile by content (new op-ID with preconditions), never blind re-execute |

Rule: nothing here is a compatibility or performance certification.
