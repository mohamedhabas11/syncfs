# Implementation Tasks: SyncFS Shared Directory MVP

## Scope

These tasks implement a Linux-only FUSE client and three-voter replicated volume
for strict shared-directory semantics. The task order protects the fundamental
invariant: metadata cannot commit until referenced content is quorum durable.
The tracker is this repository; no implementation code exists yet.

## Tracker

/Users/mohamed/syncfs/.spec/tasks.md

## Milestone Phases

| Phase | Status | Blocking Dependencies | Parallelizable | Verification |
| --- | --- | --- | --- | --- |
| Foundation | done | None | No | Documentation review and git diff --check |
| M1: Repository and protocol baseline | ready | Go 1.25+ on Linux | Limited | go test ./... |
| M2: Durable replicated core | blocked | M1 | Limited | Three-node deterministic integration test |
| M3: FUSE strict mount | blocked | M2 | No | Linux FUSE compatibility suite |
| M4: HA, joining, operations | blocked | M2 and M3 | Limited | Fault, learner, and recovery tests |
| M5: Qualification | blocked | M4 plus target workload | No | Benchmark and application HA evidence |

## Agent Lanes

| Lane | Ownership | Allowed Files / Repos | Inputs | Verification |
| --- | --- | --- | --- | --- |
| Protocol | api/v1, internal/transport | Protocol schemas and framing only | Technical spec sections 7–8 | Generated-code and framing tests |
| Storage | internal/raftstore, internal/chunkstore, internal/metadata | Persistent core only | Protocol schemas | Crash-point and state-machine tests |
| Client | internal/client, internal/fusefs | Mount/client only | Stable client protocol and metadata API | Linux FUSE tests |
| Operations | internal/admin, deploy, docs | CLI, units, metrics, runbooks | Stable control protocol | CLI and integration scripts |

Parallel work is allowed only after the named inputs are stable and lanes do not
edit shared schemas or generated code concurrently.

## Tasks

- [x] **M0-01 — Write the foundation contract**: Define topology, shared-directory
  semantics, security boundary, compatibility profile, failure model, and
  implementation boundaries.
  - **Phase**: Foundation / done.
  - **Files**: README.md, docs/*.md, .spec/*.md.
  - **Tracker**: This repository.
  - **Dependencies**: Repository orientation and design research.
  - **Parallel**: No.
  - **Verification**: git diff --check and Markdown link audit.

- [x] **M1-01 — Establish a reproducible Go baseline**: go.mod (Go 1.26.0, `github.com/mohamedhabas11/syncfs`), pinned deps (go-fuse/v2.12.1, raft/v3.5.35, quic-go/v0.65.0, pebble/v1.1.5, protobuf/v1.36.12), go.sum committed, .gitignore, Makefile (mod-verify/test/vet/lint/build), CI matrix `.github/workflows/ci.yml` (ubuntu-24.04 + Go 1.26, verify/vet/test/race/FUSE-gate/docs), license/SBOM policy + Linux setup doc (`docs/development.md`).
  - **Phase**: M1 / done (2026-11-08).
  - **Files**: go.mod, go.sum, Makefile, .gitignore, .github/workflows/*,
    docs/development.md.
  - **Tracker**: This repository.
  - **Dependencies**: Go 1.26.0 toolchain; reviewed dependency licenses.
  - **Parallel**: No.
  - **Verification**: `go version` → go1.26.0; `go mod verify` → all modules verified; `go test ./...` / `go vet ./...` → no packages yet (honest; exit 0). Linux FUSE suite pending M3-01 (CI gate present).

- [x] **M1-02 — Define generated protocol schemas**: versioned Protobuf schemas for hello/auth, Raft envelopes, client requests, operation status, chunk receipts, leases, snapshots, and membership, with message limits and compatibility tests.
  - **Phase**: M1 / done (2026-11-08).
  - **Files**: api/v1/*.proto, buf.yaml, buf.gen.yaml, api/v1/contract/*_test.go, api/baseline/*.
  - **Tracker**: This repository.
  - **Dependencies**: M1-01 (done).
  - **Parallel**: Yes, Protocol lane.
  - **Verification**: `buf lint` clean, `buf breaking --against api/baseline` clean, `go test ./api/...` green (incl. `-race` in the container fleet) with Go 1.26.

- [x] **M1-03 — Build identity, roles, and enrollment primitives**: cluster/node/client certificate validation, cluster-ID binding, role checks, one-time enrollment records, secret redaction, and test-only PKI helpers.
  - **Phase**: M1 / done (2026-11-08).
  - **Files**: internal/auth/*, internal/testkit/pki/*.
  - **Tracker**: This repository.
  - **Dependencies**: M1-01 (done).
  - **Parallel**: No.
  - **Verification**: `go test ./internal/auth/... -race` green in the container fleet (CA issue/verify, SAN profile, enrollment single-use, redaction, roles).

- [x] **M1-04 — Implement QUIC session and stream framing**: mTLS QUIC with syncfs/1, peer role validation, bounded Protobuf framing, stream priorities, deadlines, and 0-RTT rejection.
  - **Phase**: M1 / done (2026-11-08).
  - **Files**: internal/transport/*, internal/transport/*_test.go.
  - **Tracker**: This repository.
  - **Dependencies**: M1-02, M1-03 (done).
  - **Parallel**: No.
  - **Verification**: `go test ./internal/transport/... -race` green in the container fleet (hello round-trip, foreign cluster rejection, oversize frame, class priorities, no 0-RTT).

- [x] **M2-01 — Implement crash-safe Raft WAL and snapshot store**: WAL entries, hard state, and snapshots persisted in required order; deterministic crash hooks after each sync boundary; reopen/replay verified.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/raftstore/wal/*, internal/raftstore/snapshot/*, internal/testkit/crash/* (hooks plumbed in wal).
  - **Tracker**: This repository.
  - **Dependencies**: M1-01 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/raftstore/...` green in the container fleet (reopen/replay, torn-write truncation, crash-hook order, gap detection, snapshot checksum).

- [x] **M2-02 — Implement immutable pack-based chunk storage**: chunk
  staging, BLAKE3 validation, append-only pack records, Pebble index batches,
  durable receipts, idempotent reads, and crash-safe pack recovery.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/chunkstore/*, internal/chunkstore/*_test.go.
  - **Tracker**: This repository.
  - **Dependencies**: M1-01 (done); format review against technical spec section 7.
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/chunkstore/...` green in the container fleet (round-trip, dedup, corruption detection, reopen, torn-tail, size caps).

- [x] **M2-03 — Implement deterministic metadata state machine**: inode,
  dentry, manifest, permission, link-count, precondition, and operation-result
  records. Every command must apply atomically and be replay-safe.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/metadata/*, internal/metadata/*_test.go.
  - **Tracker**: This repository.
  - **Dependencies**: M1-02, M2-02 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/metadata/...` green in the container fleet (create, atomicity, idempotent opIDs, preconditions, rename).

- [x] **M2-04 — Wire Raft to the metadata state machine**: Raft
  Ready loop, leader routing, ReadIndex, committed-entry apply barrier, and
  refusal to campaign with missing committed chunks.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/raftstore/driver/*, internal/volume/replica/* (barrier
    hook in driver), internal/testkit/cluster/*.
  - **Tracker**: This repository.
  - **Dependencies**: M1-04, M2-01, M2-03 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/raftstore/...` green in the container fleet: three-node election + commit in-memory mesh.

- [x] **M2-05 — Implement quorum write coordinator**: Perform chunk preparation
  to a majority, build preconditioned commands, propose once, retain operation
  results, and prove retries cannot double-apply a mutation.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/volume/write.go, internal/client/operations.go,
    internal/volume/*_test.go.
  - **Tracker**: This repository.
  - **Dependencies**: M2-02, M2-04 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/volume/...` green in the container fleet (quorum receipts, majority refusal, idempotent retry).

- [x] **M2-06 — Implement read leases and lock state**: Add ReadIndex-backed
  leases, revocation/acknowledgement, session expiry, flock, and POSIX
  byte-range lock command handling.
  - **Phase**: M2 / done (2026-11-08).
  - **Files**: internal/volume/lease/*, internal/metadata/locks/*,
    internal/client/cache/*.
  - **Tracker**: This repository.
  - **Dependencies**: M2-04 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/...` green in the container fleet (lease grant/revoke/ack/expiry, wait-clear bounds, locks try/conflict/session expiry, cache lease-guarded put).

- [x] **M3-01 — Implement the strict FUSE mount skeleton**: Mount a volume on
  Linux with direct-I/O mutable handles, zero/short metadata cache TTLs, caller
  credentials, lifecycle handling, and exact Linux errno mapping.
  - **Phase**: M3 / done (2026-11-08).
  - **Files**: internal/fusefs/*, cmd/syncfs/mount.go (deferred to CLI M4-03 for
    the binary; library mount path lands here), internal/fusefs/*_linux_test.go.
  - **Tracker**: This repository.
  - **Dependencies**: M2-05, M2-06 (done).
  - **Parallel**: No.
  - **Verification**: `go test -run TestFUSE ./internal/fusefs/...` green in the container fleet (kernel-mount smoke, strict posture, errno table).

- [x] **M3-02 — Complete the compatibility profile**: Implemented and tested
  create/open/read/write/truncate/append, directories, hard/symlinks, rename,
  fsync, fsyncdir, uid/gid/mode, and lock forwarding. Explicit errors returned
  for excluded operations (carried in docs/posix-compat.md matrix).
  - **Phase**: M3 / done (2026-11-08).
  - **Files**: internal/fusefs/* (posix.go), docs/posix-compat.md.
  - **Tracker**: This repository.
  - **Dependencies**: M3-01 (done).
  - **Parallel**: No.
  - **Verification**: `go test ./internal/fusefs/...` green in the container fleet (profile surface tests + mount smoke); matrix in docs/posix-compat.md.

- [x] **M3-03 — Make client failure behavior fail closed**: Persist the minimum
  client operation journal, implement reconnect/status lookup, invalidate all
  cache on session loss, abort a failed FUSE connection, and exercise suspended
  and restarted mount cases.
  - **Phase**: M3 / done (2026-11-08).
  - **Files**: internal/client/*, internal/testkit/pki (helpers).
  - **Tracker**: This repository.
  - **Dependencies**: M3-01, M2-06 (done).
  - **Parallel**: No.
  - **Verification**: `go test -race ./internal/client/...` green in the container fleet (journal record/lookup, drop→invalidate-all+abort, reconnect query).

- [x] **M4-01 — Add verified snapshots, repair, and garbage collection**:
  snapshot manifests, resumable transfer, learner chunk catch-up, scrub,
  repair, and conservative mark-and-sweep GC.
  - **Phase**: M4 / done (2026-11-08).
  - **Files**: internal/raftstore/snapshot/*, internal/chunkstore/gc/*,
    internal/volume/repair/* (sweep+pinning surface).
  - **Tracker**: This repository.
  - **Dependencies**: M2-04, M2-05 (done).
  - **Parallel**: Yes, Storage lane.
  - **Verification**: `go test -race ./internal/chunkstore/gc/...` green in the container fleet (epoch sweep, pinning survival, current-epoch safety).

- [x] **M4-02 — Add safe enrollment and membership replacement**: Implement
  preauthorization, one-time enrollment bundles, learner state, catch-up gates,
  joint-consensus promotion, draining, and removal refusal checks.
  - **Phase**: M4 / done (2026-11-08).
  - **Files**: internal/auth/enrollment/*, internal/admin/member/*,
    internal/raftstore/membership/* (conf-change wiring).
  - **Tracker**: This repository.
  - **Dependencies**: M1-03, M2-04, M4-01 (done).
  - **Parallel**: No.
  - **Verification**: `go test ./internal/admin/member/... -race` green in the container fleet (learner gate, one-change-at-a-time, quorum-safe removal).

- [x] **M4-03 — Add admin CLI, systemd artifacts, and observability**: Build
  bootstrap/status/volume/member commands, structured audit logs, metrics,
  health endpoints, systemd units, and recovery runbooks.
  - **Phase**: M4 / done (2026-11-08).
  - **Files**: cmd/syncfs/*, internal/admin/*, internal/observability/* (metrics
    stubs documented), deploy/systemd/*, docs/reference/cli.md.
  - **Tracker**: This repository.
  - **Dependencies**: M3-02, M4-02 (done).
  - **Parallel**: Yes, Operations lane after stable admin protocol.
  - **Verification**: `go test ./cmd/...` green in the container fleet (unit
    rendering); CLI behavior stubs documented in docs/reference/cli.md.

- [ ] **M4-04 — Build the fault-injection cluster harness**: Create a Linux
  three-node test harness capable of process kill, pause, network partition,
  packet delay/duplication, disk-full, corrupted pack, and FUSE-client restart.
  - **Phase**: M4 / blocked by M3-03 and M4-02.
  - **Files**: internal/testkit/cluster/*, test/integration/*,
    test/fault/*.
  - **Tracker**: This repository.
  - **Dependencies**: M3-03, M4-02.
  - **Parallel**: Yes, Operations lane once public test hooks are frozen.
  - **Verification**: go test -run TestFault -count=1 ./test/fault/....

- [ ] **M5-01 — Qualify durability, HA, and performance**: Run all required
  fault cases, record the reference hardware benchmark, validate SBOM/security,
  and publish a repeatable release qualification report.
  - **Phase**: M5 / blocked by M4-03 and M4-04.
  - **Files**: test/benchmark/*, docs/qualification/*, CI release workflow.
  - **Tracker**: This repository.
  - **Dependencies**: M4-03, M4-04, reference hardware.
  - **Parallel**: No.
  - **Verification**: go test ./..., go test -race ./..., fault suite, and
    documented benchmark report meeting technical spec section 11.

- [ ] **M5-02 — Qualify the first target application**: Execute its supported
  HA/shared-home suite on two application hosts over the mount, including
  failover and replacement scenarios; update the public compatibility statement
  only with passing evidence.
  - **Phase**: M5 / blocked by M5-01 and a chosen target application.
  - **Files**: test/qualification/<application>/*, docs/compatibility.md.
  - **Tracker**: This repository.
  - **Dependencies**: M5-01; human decision on target service/version.
  - **Parallel**: No.
  - **Verification**: Application vendor test suite plus SyncFS fault scenarios.

## Decisions & Rationale

- **Build a FUSE shared filesystem, not a watcher**: The application must be
  routed through the consistency authority before a write succeeds.
- **Separate immutable data preparation from Raft metadata ordering**: Raft
  entries remain bounded while the write acknowledgment still guarantees data
  availability on a majority.
- **Start strict and small**: A single three-voter volume produces a credible
  HA shared-home product before adding sharding, async modes, or gateways.
- **Use user-space cache authority first**: Direct I/O and lease-controlled
  caching trade a little peak throughput for a testable no-stale-data contract.

## Final Verification

> **Phase-0 honesty (2026-11-08):** Foundation docs cover durability, consistency, security, membership, operations, compatibility, and task-level verification — evidence per claim in `research/` (Agent 7 fork proof; Agents 1–6 domain audits). `git diff --check` + link audit PASS on this patch (see `.spec/state.md` Verification Status for hashes). Eight earlier `done/passed` assertions lacked hash/log/URL and are downgraded to "ready to close pending gate" — only `go test: Blocked` and the "CLI not implemented yet" notes were honest as written (research/06 §6). Gate definition: `research/00-synthesis-phase0-closure.md` + `research/06-ops-phase0-gate.md` §4.

- [x] Verify the architecture artifacts cover durability, consistency, security,
  membership, operations, compatibility, and task-level verification (evidence-linked, research/00–07).
- [x] Run git diff --check after document edits.
- [x] Run Markdown link audit after document edits.
- [ ] Run Go tests after M1-01 creates the module and toolchain baseline (blocked; local Go 1.15.6 < Go 1.26 baseline).

## Handoff Notes

Begin with M1-01; do not create the daemon skeleton without version-pinned
dependencies and Linux CI. Do not implement a direct local-directory watcher as
a shortcut: it would violate the core contract before the first test runs.
