# Known limitations

Honest register. Nothing on this list is silently dropped. Items 7–13 come
from the adversarial audit in `research/01-critic-findings.md` +
`research/02-tester-evidence.md` (2026-11-08); audit fixes are listed where
landed, with residual caveats where they remain.

| # | Limitation | Impact | Recovery / path |
| --- | --- | --- | --- |
| 1 | xattr / ACL namespaces | PARTIAL | user.* get/set live with EOPNOTSUPP elsewhere; ACL policy enforcement pending |
| 2 | writable shared mmap | `EOPNOTSUPP` | none (excluded by design) |
| 3 | O_DIRECT explicitness | `EINVAL` in strict mode | use streaming reads |
| 4 | offline writes | unavailable | designed out (strict volume) |
| 5 | target-app qualification | deferred to M5 (ADR-0001) | needs reference hardware + chosen target app |
| 6 | persisted metadata index beyond Raft | in-memory scaffold | coordinator lands with M5; FUSE syncer seam implemented but production coordinator wiring pending |
| 7 | WAL snapshot + compaction | FIXED for restore/gap (snapshot restored on reopen; gap validated before write; mid-log corruption fails closed). `Compact` still refuses beyond snapshot (durable compaction pending). | manual: re-bootstrap node from verified snapshot; do not rely on `Compact` until durable |
| 8 | metadata determinism + parents | FIXED (deterministic SHA256 IDs from `(OpID, parent, name)`; parent checks in Link/Rename; Rename no-overwrite; Truncate cap; bare-xattr reject; LinkCount zombie-free) | — |
| 9 | chunkstore corruption + dedup | FIXED for mid-pack skip + dedup verify (+ bounds + nil-pack guards). `TestTornWriteRecovered` still tolerates torn tail as `ErrCorrupt` (quarantine is skip, not forensic copy). | manual: scrub + repair from majority |
| 10 | enrollment + role enforcement | PARTIAL (empty-proof rejected, `MarkConsumed` guards double-consume; `VerifyConnection` wired (`VerifyConnectionFunc`) + per-stream `AllowedPrincipalForClass`/`AcceptClassifiedStream` enforced. `Consume` Get-then-Mark TOCTOU remains for generic stores; opener-side `ValidatePrincipalForClass` is caller duty — `OpenClassifiedStream` does not yet auto-check the opener role (QUIC `ConnectionState` exposes peer only). | manual: rotate enrollment nonces; openers must call `ValidatePrincipalForClass` before `OpenClassifiedStream` |
| 11 | volume coordinator | FIXED for sentinel/ctx/batch (wraps `ErrNoQuorum`; honors `ctx`; pre-validates batch; monotonic RaftIndex) | orphan sweep via GC remains the safety net |
| 12 | transport framing | FIXED for zero-length + per-class caps + single-Writer lock | — |
| 13 | benchmark qualification gap | numbers are in-process / Docker VM only (no QUIC/FUSE/10 GbE); §11 targets UNVERIFIED | run §11 gates on reference hardware — see `docs/benchmarks/README.md` |
| 14 | driver chunk-gap gating | FIXED for explicit campaign + deferred apply, non-blocking `Step`, voter validation (`TestApplyDeferredForMissingChunk`, `TestCampaignRefusedWithChunkGap`). Auto-tick elections are raft-internal: callers must consult `ChunkGap` after election before serving writes; volume layer does not yet wire this check. | fetch+verify missing chunks, call `RetryApply`; review driver tests before production use |
| 15 | FUSE inode tree + open flags | PARTIAL (nil-handler fails closed (`TestNilHandlerFailsClosed`), `O_DIRECT` → `EINVAL` on Linux via `oDirectFlag` (build-tagged). `Lookup` still returns nil inode with entry attrs; full go-fuse child tree (`NewPersistentInode`, `Readdir`, `Forget`) is pending — kernel use is stat-root only until then. | mount with a real root+child inode tree; test `ls` via `/dev/fuse` |
| 16 | blocking locks | FIXED for cancellable wait + deadlock + wake (`BlockingGrantCtx`, `ErrDeadlock`, `queues[(Inode,Family)]`, `ApplyReleases` wake; tests `TestBlockingGrantCancelled`, `TestDeadlockDetected`, `TestFamilyQueueIsolation`). `BlockingGrant` remains as the unbounded wrapper for compat. | callers should migrate to `BlockingGrantCtx` with a deadline |

Rule: nothing here is a compatibility or performance certification.
