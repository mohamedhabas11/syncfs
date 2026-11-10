# Status

Honest per-milestone state. Allowed: NOT_STARTED | PARTIAL | DONE | DEFERRED | BLOCKED.

| Milestone | State | Evidence |
| --- | --- | --- |
| Phase 0 foundation | DONE | `research/00-synthesis-phase0-closure.md`, D-001 (`.spec/decisions.md`) |
| M1-01 Go baseline | DONE | `go mod verify` — "all modules verified" (788f7af) |
| M1-02 api/v1 schemas | DONE | `buf lint` clean, `buf breaking` clean, `go test ./api/...` PASS (9c35f54) |
| M1-03 auth/PKI | DONE | `go test ./internal/auth/... -race` PASS (2a7804c); partial caveats in `docs/known-limitations.md` #10 |
| M1-04 QUIC transport | DONE | `go test ./internal/transport/... -race` PASS (720ff6d); framing fix #12 |
| M2-01 WAL/snapshot | DONE | `go test ./internal/raftstore/... -race` PASS (2645e09); restore/gap fixed #7; **2026-11-10 execution review: conflicting-suffix overwrite + gap wedge and mid-log corruption fail-closed pending fixes queued in tasks Iter-8-03 / Iter-8-04** |
| M2-02 chunkstore | DONE | `go test ./internal/chunkstore/... -race` PASS (7a261f7); corruption/dedup fixed #9 |
| M2-03 metadata | DONE | `go test ./internal/metadata/... -race` PASS (1403df6); determinism/parents fixed #8; persisted projection `Encode`/`Decode`/`Persist`/`LoadPersisted` with checksum + atomic write (`TestPersistRoundTrip`) for T06 |
| M2-04 raft driver | DONE | `go test ./internal/raftstore/... -race` PASS incl. 3-node mesh (c89af8c); chunk-gap gating + deferred apply + voter validation + tick-gated campaign (`TestGappedTickDoesNotElect`: log-wide gap scan (committed+uncommitted), no-campaign, vote-grant ungated) + volume serve-gate (`TestVolumeConsultsRealDriverChunkGap`, `TestGappedLeaderCannotServe`) |
| M2-05 quorum writes | PARTIAL | `go test ./internal/volume/... -race` PASS (9ae7f7b); sentinel/ctx/batch/`PeerID`/receipt quorum fixed #11; Raft commit via `SetRaftProposer` (`TestProposeViaRaftCommit`) + lease revoke `WaitClear` wired (`TestProposeRevokesLeases`) — direct-Apply fallback remains for metadata-only tests (#17). **2026-11-10 execution review: leader acknowledged `COMMITTED` without apply barrier — fix queued in tasks Iter-8-01** (waiter keyed by proposed entry, apply only from apply loop, ack on applied index). Daemon composition (`internal/daemon`) now recovers WAL+metadata (see M4-03 tail) but FUSE→coordinator binding (T03) remains. |
| M2-06 leases/locks/cache | DONE | `go test ./... -race` PASS (c8fc0eb) + locks fix (ecb37e7): `TestFamilySeparationIsTotal`, `TestSameOwnerNeverSelfBlocks`, `TestBlockingGrantAcquiresAfterRelease`, `TestBindProtoCarriesInodeAndRange`; generation + `WaitClear` + `InvalidateAll` clears leases + E2E `LOCK_GRANT` via `Apply` (`TestLockGrantAppliesE2E`) |
| M3-01 FUSE skeleton | DONE | `TestFUSEmountSmoke` PASS in container (eba1e4e; completion 1800055: `TestStrictTimeoutsZero`, `TestCallerCredentialsEnforcedInLookup`, `TestWriteFileFailsClosedOnSyncerError`, `TestOpenReleaseFsyncLifecycle`, `TestErrnoTable` PASS); caveats #15 |
| M3-02 POSIX compat | PARTIAL | `TestCompatProfileSurface` + `TestHardLinkSymlinkFsyncDir` + `TestNestedTree` PASS for in-memory surface; `Root.Lookup` returns `childNode` + `Root.Readdir` for flat-root children, `childNode.Lookup`/`Readdir` now walk arbitrary depth in-memory (`a/b/c` via `tree.go`); `Forget`, cross-dir rename, metadata inode-ID binding pending (#15); matrix `docs/posix-compat.md` |
| M3-03 client failure | DONE | `go test ./internal/client/... -race` PASS (175e4e3) + durable journal (31ecfb2): `TestJournalPersistsAcrossRestart`, `TestReconnectFoldsServerState`, `TestDropInvalidateSuspended`, TTL/prune (`TestJournalTTLExpiry`) |
| M4-01 snapshots+GC | PARTIAL | `go test ./internal/chunkstore/gc/... -race` PASS (06d7450); epoch grace (`oldestRetained`) + `SweepAndDelete`/`Store.Delete` + `ComposeReachability` pins at library level; v2 envelope with cluster/volume binding + v0/v1 back-compat (`WriteSnapshotFileWithIDs`); callers composing pins + resumable transfer pending |
| M4-02 membership | PARTIAL | `go test ./internal/admin/member/... -race` PASS (0c488d5) + joint (a915888: `TestJointPromotionFlow`, `TestReplacementFlow`); pending now held until `ConfirmPromotion`/`ConfirmJointLeave`/`ConfirmRemove` (single in-flight, Inv.8) + `Quorum()` counts only promoted (`TestQuorumExcludesLearners`); ConfState driver still convention |
| M4-03 CLI/systemd | PARTIAL | `go test ./cmd/... ./internal/daemon/... -race` PASS; daemon is now a real composition root (`internal/daemon`: typed config with `DisallowUnknownFields`, recovery-first WAL+chunk+metadata replay + `LoadPersisted`, `/healthz`/`/readyz`/`/metrics` from live state, bounded shutdown `ShutdownTimeout`); admin commands remain validate-and-print stubs (no control-plane proposals yet, #17). `TestThreeDaemonClusterRecovery` + `TestDaemonGracefulShutdownBounded` prove recovery/restart/shutdown; QUIC 3-process election proof is pending (in-memory mesh remains election evidence) — **2026-11-10 execution review: admin `--op promote` / `volume --op create` print success and exit 0 without acting; `mount` without `--daemon` exits 2 (shipped systemd mount unit cannot start); `request_deadline` duration string rejected; `main.version` missing; gofmt `-s` gate failing — all queued in tasks Iter-8-08** |
| M4-04 fault harness | PARTIAL | `go test ./internal/testkit/...` PASS (66bd4db): fault model vocabulary (8 kinds) + history/realizability + Mesh injection (`SetFaults`, `TestPartitionIsolates`); `testkit/pki` coverage and Docker realization deferred to M5 |
| M5-01 durability/HA/perf qualification | DEFERRED | ADR-0001 (docs/adr/0001-defer-m5-qualification.md) — needs reference hardware; harness + in-process numbers in `docs/benchmarks/` only |
| §11 perf 4 KiB quorum write 1,000 ops/s p99 ≤ 25 ms | UNVERIFIED | needs 3× NVMe honoring flushes (ext4 `data=ordered`, barriers on, `commit=5s`) + 10 GbE low-latency, Pebble `Sync`; Docker Desktop numbers in `docs/benchmarks/floor.md`/`sweep.md` NEVER satisfy this |
| §11 perf create/write/rename 500 ops/s p99 ≤ 30 ms | UNVERIFIED | same reference hardware as above |
| §11 perf cache-hit strict reads p99 ≤ 2 ms | UNVERIFIED | same reference hardware as above; no QUIC/lease measurement exists here |
| M5-02 target-app qualification | DEFERRED | Same ADR; needs a real HA suite target (HUMAN_TODO) |
| Observability (M4-03 tail) | PARTIAL | `go test ./internal/observability/... -race` PASS; `Handler` now splits liveness `/healthz` (200 while serving) vs readiness `/readyz` (503 without leader/quorum, `TestHandlerMetricsHealthz`); daemon (`internal/daemon`) serves both + `/metrics` from live Raft/WAL state (`Ready()` = recovered + leader + no chunk gap) on a 1s ticker |
| Xattr user.* surface | DONE | `TestXattrUserNamespaceOnly` (user.* with non-empty suffix live; ACL pending, #1) |

Release readiness: **v0.1.0 pre-1.0, foundation complete; not production ready**.
Nothing here is a compatibility or performance certification.
See `docs/known-limitations.md` for durability caveats (#7–#23) and
`docs/posix-compat.md` for the per-operation matrix.

2026-11-10: post-implementation execution review confirmed-by-execution P0/P1 bugs
(leader quorum-commit ack without apply barrier, daemon leader-election tick loop,
WAL conflicting-suffix overwrite/gap, WAL mid-log corruption, HTTP data plane,
TLS SANs/CA key/rotation, FUSE→daemon write path) plus read-only hygiene gaps.
Work packages `Iter-8-01`..`Iter-8-10` are queued in `.spec/tasks.md` §"Iteration 8";
the `RELEASE READY v0.1.0` claim in `PROGRESS.md` has been withdrawn until the Iter-8
end-to-end test exists.
