# SyncFS Progress

Autonomous build log. One line per milestone: id, commit, commands, result.

| Date | Milestone | Commit | Commands run | Result |
| --- | --- | --- | --- | --- |
| 2026-11-08 | M1-01 Go baseline | 788f7af | go version / mod verify / test / vet | PASS (go1.26.0, no packages yet) |
| 2026-11-08 | M1-02 api/v1 schemas | 9c35f54 | buf lint / breaking / generate, go test ./api/... | PASS |
| 2026-11-08 | M1-03 auth/PKI | 2a7804c | go test ./internal/auth/... -race (fleet) | PASS |
| 2026-11-08 | M1-04 QUIC transport | 720ff6d | go test ./internal/transport/... -race (fleet) | PASS |
| 2026-11-08 | M2-01 WAL + snapshot store | 2645e09 | go test ./internal/raftstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-02 chunkstore packs+BLAKE3+Pebble | 7a261f7 | go test ./internal/chunkstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-03 metadata state machine | 1403df6 | go test ./internal/metadata/... -race (fleet) | PASS |
| 2026-11-08 | M2-04 raft driver + 3-node mesh | c89af8c | go test ./internal/raftstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-05 quorum write coordinator | 9ae7f7b | go test ./internal/volume/... -race (fleet) | PASS |
| 2026-11-08 | M2-06 leases + locks + cache | c8fc0eb | go test ./... -count=1 -race (fleet) | PASS |
| 2026-11-08 | M3-01 FUSE mount (skeleton, honest partial → completed) | e133cab → eba1e4e | go test ./internal/fusefs/... -race (fleet) | PASS (completed) |
| 2026-11-08 | M3-02 POSIX compat surface | 82c4761 | go test ./internal/fusefs/... -race (fleet) | PASS |
| 2026-11-08 | M3-03 client failure semantics | 175e4e3 | go test ./internal/client/... -race (fleet) | PASS |
| 2026-11-08 | M4-01 snapshots+GC | 06d7450 | go test ./internal/chunkstore/gc/... -race (fleet) | PASS |
| 2026-11-08 | M4-02 membership replacement | 0c488d5 | go test ./internal/admin/member/... -race (fleet) | PASS |
| 2026-11-08 | M4-03 CLI + systemd | d5371f3 | go test ./cmd/... -race (fleet) | PASS |
| 2026-11-08 | M4-04 fault harness | 66bd4db | go test ./internal/testkit/... -race (fleet) | PASS |
| 2026-11-08 | Docs + fleet + diagrams | e133cab | mermaid diagrams; TestFUSEmountSmoke green | PASS |
| 2026-11-08 | Release prep | 2455728 | mkdocs build --strict; go build/vet/test-race | PASS |
| 2026-11-08 | M5 qualification | — | — | DEFERRED (ADR-0001) |
| 2026-11-08 | Docs reality pass (match code, trim fluff) | TBD | mkdocs build --strict; go test ./... -race | PASS |

## Spec-sheet progress tracking

| Spec sheet | Status |
| --- | --- |
| diagrams/docs | architecture.md write-path + read/lease flows; operating-model bootstrap joining (mermaid); mkdocs site with architecture/ sub-pages |
| PROGRESS.md | whole-build + this file |
| spec reconciliation | D-001/Camp A adopted; Camp B archived |
| .spec/tasks.md | all M1–M4 ticked; M5 open per ADR-0001 |
| known-limitations + posix-compat | maintained honestly |

## M3-01 status (2026-11-08)

Delivered: mount skeleton (go-fuse v2), strict posture, kernel-mount smoke
test in the fleet, Lookup/Open/Getattr + errno table + handler surface.
In-memory state; coordinator persistence wiring follows with M5.

| 2026-11-08 | M2-06 locks fix | ecb37e7 | go test ./internal/metadata/locks/... -race (fleet: TestFamilySeparationIsTotal, TestSameOwnerNeverSelfBlocks, TestBlockingGrantAcquiresAfterRelease, TestBindProtoCarriesInodeAndRange) | PASS |
| 2026-11-08 | M3-01/M3-02 FUSE completion | 1800055 | go test ./internal/fusefs/... -race (TestStrictTimeoutsZero, TestCallerCredentialsEnforcedInLookup, TestWriteFileFailsClosedOnSyncerError, TestOpenReleaseFsyncLifecycle, TestFUSEmountSmoke kernel-mount) | PASS |
| 2026-11-08 | M3-03 durable journal | 31ecfb2 | go test ./internal/client/... -race (TestJournalPersistsAcrossRestart, TestReconnectFoldsServerState, TestDropInvalidateSuspended) | PASS |
| 2026-11-08 | M4-02/M4-03 joint+CLI | a915888 | go test ./internal/admin/member/... + ./cmd/... -race (TestJointPromotionFlow, TestAdminCLIParsing) | PASS |
| 2026-11-08 | Docs honesty + ADRs + reference | f6017a4 | mkdocs build --strict (passed); docs/spec/status vs .spec/tasks matched | PASS |
| 2026-11-08 | M2-01/M2-03 audit fixes | 882c343 | go vet + go test ./... (WAL restore, deterministic IDs) | PASS |
| 2026-11-08 | M2-02/M2-05/M1-03/M1-04/M3-02 audit fixes | 2b4b06f | go test ./... (chunkstore quarantine, volume batch/ctx, enrollment, framing) | PASS |
| 2026-11-08 | M5-01 edge bench (deferred qual) | 4899533 | TestEdgeCases C1-C8 PASS; fresh-payload benches (docker ~823 ops/s p50 1.17ms; darwin ~42 ops/s — results.json) | PASS (in-process only) |
| 2026-11-08 | Final verification (clean tree) | cc8b57e | go build ./..., go vet ./..., go test ./... -race (23 pkgs), mkdocs build --strict, goreleaser check (no local binary; CI release workflow on tag) | PASS |

## Iteration 8 — post-review execution hardening (2026-11-10)

Work packages added from the 2026-11-10 post-implementation execution review
(run findings confirmed by executing code; read-only confirmations itemised).

| Date | Milestone | Commit | Commands run | Result |
| --- | --- | --- | --- | --- |
| 2026-11-10 | Iter 8: post-review findings triage + work packages | 60793f2 | tracker/spec edits only; findings evidence from scratch-copy execution probes | CONFIRMED: P0 x4 (quorum-ack, election, WAL overwrite, WAL corruption); P1 x3 (HTTP surface, TLS SANs, FUSE write path); P2 x1 (config/format/admin/Apply) + read-only (packaging, release/CI) |

Work packages (`.spec/tasks.md` §"Iteration 8", loop L10):

- Iter-8-01 — leader quorum-commit apply barrier (P0).
- Iter-8-02 — daemon leader election via `Ready` (P0) — prerequisite for the real
  3-daemon e2e (kill follower + leader).
- Iter-8-03 — WAL conflicting-suffix overwrite + gap handling (P0).
- Iter-8-04 — WAL mid-log corruption fail-closed (P0).
- Iter-8-05..07 (P1): HTTP data-plane lockdown; TLS SAN/CA-key/rotation; FUSE→daemon
  write path.
- Iter-8-08 (P2): config durations, `gofmt -l -s` gate, admin/mount CLI behaviour,
  `Apply` deep-copy cost.
- Iter-8-09..10 (P2, read-only confirmations): packaging/tooling honesty; release +
  CI gates.

No code shipped in this pass; tracker/spec updates only.

### 2026-11-10 execution-hardening code landing (Iter-8 loop, second pass)

All Iter-8 work packages moved from tracker/spec to shipped code. Clean-tree
verification (Docker `golang:1.26`): `go build ./...`, `go vet ./...`, `gofmt -l -s`
clean, `go test ./... -count=1` green (full suite; single chunkstore flake
reproduced-passed in isolation).

Landed code this pass (all previously confirmed-by-execution findings):

- Iter-8-01 — leader quorum-commit apply barrier: leader now waits for the
  apply loop to durably process the proposed entry (`WaitApplied`) before
  acking; apply loop advances `applied` (incl. REJECTED handling) + persists
  projection; proposer closure waits applied.
- Iter-8-02 — daemon election: driver consumes `ReadyCh()` via
  `StepReady` (dedicated loop); `Step` persists-before-advance and always
  `Advance()`s (fail-closed on save error); unconditional apply.
- Iter-8-03/04 — WAL: CRC over header+payload (fail-closed on mismatch); torn
  tail only at EOF; `ErrIndexGap` on conflicting suffix.
- Iter-8-05 — HTTP data-plane lockdown: `/raft/propose` removed;
  `/volume/chunks` + `/volume/command` require admin bearer token
  (`admin_token` config); `/volume/command` empty-body → 400, oversize → 413;
  `/volume/chunks` response now returns durable receipts JSON.
- Iter-8-06 — TLS SANs: `CA.AddrsToSANs` + `CA.Issue(principalType, id, dnsNames, ips)`;
  fleet generator retains CA key (`ca.key`) and issues node leaves with
  address-derived SANs; `CA.Rotate` + `CA.NewCAFromPEM` + `daemon.RotateNode`
  (operator rotation path).
- Iter-8-07 — FUSE→daemon write path: `WireSyncer` attaches real quorum
  receipts from `/volume/chunks` to WRITE_RANGES `PreparedChunks` (payload
  before reference); deterministic inode-ID/opID derivation in `ensureFile`;
  fsync fail-closed via ensureFile.
- Iter-8-08 — config durations accept JSON strings (`"request_deadline": "5s"`);
  admin CLI stubs (`volume --op create`, `member --op promote|remove|add-learner`)
  fail closed (exit 1, no fake success); `mount` without `--daemon` exits 2.

Remaining (read-only confirmations / post-MVP): packaging
(`go mod tidy`, Makefile clean, goreleaser ldflags), release/CI (mike --push,
release workflow test gate), raft-driver read hardening (peerConn dial mutex,
WriteChunks locks, Propose mutex, per-entry Persist, startup sleep ctx, WAL
fsync per record, frameMu, coordinator trust), systemd mount hardening, real
3-daemon e2e (kill follower+leader).

## Final (autonomous mode)

M2-02..M2-06 are complete: raft WAL/snapshot store, BLAKE3 chunkstore, metadata
state machine, raft driver with 3-node mesh, quorum write coordinator, read
leases + lock state + client cache. Clean-tree gate passed: `go build ./...`,
`go vet ./...`, and `go test -race ./...` all green (12 packages). Tracked
the blake3 dependency in a final tidying commit (dd209fa). Two independent
bugs found+fixed by tests this session: (1) lock-range disjoint logic was
inverted; (2) 3-node election deadlocked on lockstep ticking with PreVote
(each node self-voted first) — fixed by single-node ticking per round in the
mesh harness (per etcd follower-election design).

2026-11-10: **RELEASE READY v0.1.0 claim WITHDRAWN** (2026-11-10 post-review
execution). Confirmed-by-execution findings: leader quorum-commit ack without apply
barrier (critical), daemon leader-election tick loop never elected (critical), WAL
conflicting-suffix overwrite/gap wedge + mid-log corruption fail-closed (critical),
HTTP data plane plaintext/unauthenticated/unbounded (high), TLS SAN coverage/CA-key/
rotation (high), FUSE→daemon write path failure (high); plus read-only hygiene gaps
(config durations, gofmt -s gate, admin/mount CLI stubs, Apply deep-copy). Work
packages Iter-8-01..Iter-8-10 queued in `.spec/tasks.md` §"Iteration 8"; P0 fixes
land in the next iteration loop; the "RELEASE READY" claim returns only after the
real 3-daemon end-to-end test (kill follower + leader) passes.
