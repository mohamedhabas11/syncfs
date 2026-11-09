# Roadmap: SyncFS

Date: 2026-11-08
Tracker repo: /Users/mohamed/syncfs
Current baseline: M1 done; M2–M4 partial (see `docs/spec/status.md`);
M5 deferred under ADR-0001. (Evaluator reconciliation 2026-11-09: the prior
“M1–M4 implemented” line overstated M2-05, M3-02, M4-01..04, which are PARTIAL.)

## Purpose

Deliver a small, reliable shared-directory system for HA services without
drifting into a capacity-oriented distributed-storage project. Each milestone
raises a measurable correctness threshold before adding scope.

## Milestone Table (as of 2026-11-08)

| Order | Milestone | Status | Verification |
| --- | --- | --- | --- |
| 0 | Architecture foundation | Done | Documentation audit |
| 1 | Strict replicated core | PARTIAL (M1 done; M2-05 partial) | Three-node mesh + unit suites |
| 2 | Linux shared mount | PARTIAL (M3-02 flat-root) | FUSE mount + compat surface |
| 3 | HA operations and joining | PARTIAL (M4-01..04) | GC/membership/CLI/fault suites |
| 4 | Workload qualification | DEFERRED (ADR-0001) | needs reference hardware |
| 5 | Selective expansion | Later | ADR + milestone tests |

## Next Formal Milestone

### Objective

Create a reproducible development baseline and a deterministic replicated core
that proves the payload-before-metadata invariant before a FUSE mount is added.

### Scope

- Pin a Go 1.25+ Linux toolchain and reviewed dependencies.
- Define and test versioned protocol schemas.
- Implement crash-safe WAL, snapshot, immutable chunk packs, metadata commands,
  and a deterministic three-node Raft harness.
- Prove duplicate/retried operations do not double-apply and no committed
  manifest points at data missing from a surviving majority.

### Acceptance Criteria

- A clean Linux checkout builds and runs the focused unit suite with locked
  dependency versions.
- Injected crashes at each WAL/chunk/metadata boundary recover to either the
  old committed state or the new committed state, never a mixed state.
- Three simulated nodes retain acknowledged mutations after any one node is
  stopped and reject writes from a partition minority.

### Initial Task Breakdown

| ID | Task | Primary Files / Repos | Verification |
| --- | --- | --- | --- |
| M1-01 | Reproducible Go baseline | go.mod, CI, development docs | go test ./... |
| M1-02 | Protocol schemas | api/v1/* | Schema compatibility tests |
| M2-01 | WAL and snapshot store | internal/raftstore/* | Crash/reopen tests |
| M2-02 | Chunk storage | internal/chunkstore/* | Torn-write/hash tests |
| M2-03 | Metadata state machine | internal/metadata/* | Deterministic property tests |
| M2-04/05 | Raft and quorum writes | internal/volume/* | Three-node invariant suite |

## Later Milestones

### Linux shared mount

Implement a strict FUSE mount only after the replicated core has proof-quality
tests. Begin with direct I/O for mutable files and userspace cache leases; do
not optimize kernel page-cache behavior before suspension/disconnect tests pass.

### HA operations and joining

Add cluster bootstrap, offline-capable CA workflow, mTLS transport, metrics,
snapshots, learner admission, promotion, repair, and safe replacement. This
waits because joining is only safe once the persisted volume format and snapshot
invariant are stable.

### Application qualification

Choose one target, for example a specific Jira/Confluence data-center version
or repository manager, and capture its required operations as integration
tests. Do not label any workload supported beforehand.

### Selective expansion

Potential future decisions, each requiring an ADR and a separate acceptance
plan:

- additional independent volumes and placement groups;
- read-only NFS/SMB gateway or protocol-specific integration;
- more storage voters and replica placement;
- application-layer encryption/KMS integration;
- offline/eventual mode as a separate product tier; and
- optimized kernel page cache or mmap coherence.

## Decision Log

- **Stay focused on shared directories**: Object storage, distributed capacity,
  and WAN replication would obscure the first product's correctness goals.
- **Use a direct FUSE mount**: It is required to mediate writes and cache
  visibility before success reaches an application.
- **Require a three-node quorum**: It gives an understandable one-node-failure
  model and avoids unsafe two-node promotion choices.
- **Qualify before broad support claims**: Service names identify workload
  classes, not a completed vendor compatibility certification.
