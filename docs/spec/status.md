# Status

Honest per-milestone state. Allowed: NOT_STARTED | PARTIAL | DONE | DEFERRED | BLOCKED.

| Milestone | State | Evidence |
| --- | --- | --- |
| Phase 0 foundation | DONE | `research/00-synthesis-phase0-closure.md`, D-001 (`.spec/decisions.md`) |
| M1-01 Go baseline | DONE | `go mod verify` — "all modules verified" (788f7af) |
| M1-02 api/v1 schemas | DONE | `buf lint` clean, `buf breaking` clean, `go test ./api/...` PASS (9c35f54) |
| M1-03 auth/PKI | DONE | `go test ./internal/auth/... -race` PASS (2a7804c); partial caveats in `docs/known-limitations.md` #10 |
| M1-04 QUIC transport | DONE | `go test ./internal/transport/... -race` PASS (720ff6d); framing fix #12 |
| M2-01 WAL/snapshot | DONE | `go test ./internal/raftstore/... -race` PASS (2645e09); restore/gap fixed #7 |
| M2-02 chunkstore | DONE | `go test ./internal/chunkstore/... -race` PASS (7a261f7); corruption/dedup fixed #9 |
| M2-03 metadata | DONE | `go test ./internal/metadata/... -race` PASS (1403df6); determinism/parents fixed #8 |
| M2-04 raft driver | DONE | `go test ./internal/raftstore/... -race` PASS incl. 3-node mesh (c89af8c); chunk-gap gating + deferred apply + voter validation (`TestApplyDeferredForMissingChunk`, `TestCampaignRefusedWithChunkGap`); caveats #14 |
| M2-05 quorum writes | DONE | `go test ./internal/volume/... -race` PASS (9ae7f7b); sentinel/ctx/batch fixed #11; no group commit yet (#17) |
| M2-06 leases/locks/cache | PARTIAL | `go test ./... -race` PASS (c8fc0eb) + locks fix (ecb37e7): `TestFamilySeparationIsTotal`, `TestSameOwnerNeverSelfBlocks`, `TestBlockingGrantAcquiresAfterRelease`, `TestBindProtoCarriesInodeAndRange`; manager GREEN (generation check, `WaitClear`, `InvalidateAll` clears leases) but Raft-ordered lock E2E wiring pending (#16) |
| M3-01 FUSE skeleton | DONE | `TestFUSEmountSmoke` PASS in container (eba1e4e; completion 1800055: `TestStrictTimeoutsZero`, `TestCallerCredentialsEnforcedInLookup`, `TestWriteFileFailsClosedOnSyncerError`, `TestOpenReleaseFsyncLifecycle`, `TestErrnoTable` PASS); caveats #15 |
| M3-02 POSIX compat | PARTIAL | `TestCompatProfileSurface` + `TestHardLinkSymlinkFsyncDir` PASS (82c4761; additions 1800055) for the in-memory Handler surface; kernel traversal (full inode tree, `Readdir`, `Forget`) pending (#15); matrix `docs/posix-compat.md` |
| M3-03 client failure | DONE | `go test ./internal/client/... -race` PASS (175e4e3) + durable journal (31ecfb2): `TestJournalPersistsAcrossRestart`, `TestReconnectFoldsServerState`, `TestDropInvalidateSuspended`, TTL/prune (`TestJournalTTLExpiry`) |
| M4-01 snapshots+GC | DONE | `go test ./internal/chunkstore/gc/... -race` PASS (06d7450); epoch grace (`oldestRetained`) implemented |
| M4-02 membership | DONE | `go test ./internal/admin/member/... -race` PASS (0c488d5) + joint (a915888: `TestJointPromotionFlow`, `TestReplacementFlow`); removal refused at 3 voters |
| M4-03 CLI/systemd | DONE | `go test ./cmd/...` PASS (d5371f3) + validation (a915888: `TestAdminCLIParsing`, `TestRenderUnit`); systemd units `deploy/systemd/`; binaries are validate-and-print stubs, no control-plane proposals (#17, `docs/reference/cli.md`) |
| M4-04 fault harness | DONE | `go test ./internal/testkit/...` PASS (66bd4db): 8 fault kinds (kill/pause/partition/delay/duplicate/disk-full/corrupt-pack/client-restart) + history/realizability; Docker realization deferred to M5 |
| M5-01 durability/HA/perf qualification | DEFERRED | ADR-0001 (docs/adr/0001-defer-m5-qualification.md) — needs reference hardware; harness + in-process numbers in `docs/benchmarks/` only |
| §11 perf 4 KiB quorum write 1,000 ops/s p99 ≤ 25 ms | UNVERIFIED | needs 3× NVMe honoring flushes (ext4 `data=ordered`, barriers on, `commit=5s`) + 10 GbE low-latency, Pebble `Sync`; Docker Desktop numbers in `docs/benchmarks/floor.md`/`sweep.md` NEVER satisfy this |
| §11 perf create/write/rename 500 ops/s p99 ≤ 30 ms | UNVERIFIED | same reference hardware as above |
| §11 perf cache-hit strict reads p99 ≤ 2 ms | UNVERIFIED | same reference hardware as above; no QUIC/lease measurement exists here |
| M5-02 target-app qualification | DEFERRED | Same ADR; needs a real HA suite target (HUMAN_TODO) |
| Observability (M4-03 tail) | PARTIAL | `go test ./internal/observability/... -race` PASS for the in-memory Prometheus-text collector (`internal/observability/observability.go`); daemon wiring is M5 work |
| Xattr user.* surface | DONE | `TestXattrUserNamespaceOnly` (user.* with non-empty suffix live; ACL pending, #1) |

Release readiness: **v0.1.0 pre-1.0, foundation complete; not production ready**.
Nothing here is a compatibility or performance certification.
See `docs/known-limitations.md` for durability caveats (#7–#17) and
`docs/posix-compat.md` for the per-operation matrix.
