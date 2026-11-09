# SyncFS — production-readiness assessment



Not production-ready

Qualification incomplete



Assessment date: 9 November 2026\
Repository reviewed: [mohamedhabas11/syncfs](https://github.com/mohamedhabas11/syncfs?utm_source=chatgpt.com)

## 1. Executive assessment

The repository has a substantial engineering foundation, but it is not yet a production-usable shared filesystem.

The most important distinction is between implemented internal components and a working, supported end-to-end storage service.

The README reports that M1–M4 are implemented and that the build, vet, and race-test checks pass. However, the POSIX compatibility document says the FUSE implementation is still a flat root-children tree, with kernel traversal incomplete. The operating model also explicitly describes the administrative CLI as a stub that prints intended control-plane actions rather than executing them.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://github.com\&sz=32)

GitHub+2



That is a functional product gap, not merely production polish.

My recommendation is to finish the existing architecture before adding features. Do not expand the scope into another distributed filesystem, NFS/SMB server, or general-purpose storage platform.

The target should be a narrowly qualified, three-node Linux shared-directory service with a genuinely usable FUSE mount, predictable failure handling, safe operations, and demonstrable durability.

## 2. What the repository currently promises

The current design is quite specific:

| Area             | Declared design                                           |
| ---------------- | --------------------------------------------------------- |
| Storage topology | Three voting storage nodes                                |
| Backend          | Private local ext4/XFS                                    |
| Consistency      | Strong, quorum-based                                      |
| Replication      | Raft + quorum-durable chunks                              |
| Client access    | Linux FUSE mount                                          |
| Transport        | Mutual TLS 1.3 over QUIC                                  |
| Write format     | 64 KiB copy-on-write chunks                               |
| Joining          | Non-voting learner, snapshot verification, then promotion |
| Target workload  | Small, high-value shared directories                      |

These are explicit design decisions in the README and technical specification.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://github.com\&sz=32)

GitHub+3



The scope boundary is sensible: no WAN replication, no two-node strong cluster, no direct backing-directory access, no NFS/SMB serving, and no writable shared `mmap`.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://github.com\&sz=32)

GitHub+1



The architecture is therefore not inherently too ambitious for its stated use case. The challenge is proving that its consistency and POSIX contracts hold under real Linux workloads and failures.

## 3. Critical findings, ranked by severity

P0 — Blocks any production deployment

| ID   | Finding                             | Evidence and consequence                                                                                         |
| ---- | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| F-01 | No production data path             | `NewStrictHandler()` has no coordinator. Production writes cannot be quorum-committed through it.                |
| F-02 | Fsync is not durable                | `fileHandle.Fsync()` returns success without flushing anything. `Handler.Fsync()` is also a no-op.               |
| F-03 | Incomplete filesystem tree          | FUSE currently exposes a flat root-level structure. Nested directory traversal is unfinished.                    |
| F-04 | No functional storage daemon        | `runDaemon()` starts a metrics HTTP server, not the Raft-backed storage service.                                 |
| F-05 | Administrative operations are stubs | Bootstrap, status, volume and membership commands print descriptions instead of performing operations.           |
| F-06 | Metadata durability incomplete      | Metadata persistence beyond the Raft log is marked as an in-memory scaffold. Durable compaction is also pending. |

The evidence is particularly clear in these files:

- [`internal/fusefs/fusefs.go`](https://github.com/mohamedhabas11/syncfs/blob/main/internal/fusefs/fusefs.go?utm_source=chatgpt.com): in-memory handler, test-only no-op syncer and strict handler.
- [`internal/fusefs/posix.go`](https://github.com/mohamedhabas11/syncfs/blob/main/internal/fusefs/posix.go?utm_source=chatgpt.com): non-durable truncate and fsync implementation.
- [`cmd/syncfs/main.go`](https://github.com/mohamedhabas11/syncfs/blob/main/cmd/syncfs/main.go?utm_source=chatgpt.com): command execution and daemon composition.
- [`docs/known-limitations.md`](https://github.com/mohamedhabas11/syncfs/blob/main/docs/known-limitations.md?utm_source=chatgpt.com): limitations 6 and 7.

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+5

Important: The strict handler failing closed is preferable to silently acknowledging non-durable writes. Do not weaken that behaviour to make the mount appear functional. Wire the actual coordinator into it.

P0 — Security blockers

There are two unresolved authentication issues:

1. Enrollment consumption has a Get-then-Mark time-of-check/time-of-use race.
2. The transport stream-opening API does not automatically validate the opener's role. Its caller must remember to invoke `ValidatePrincipalForClass()`.

These are acknowledged in limitation 10. Move authorization enforcement into the transport API itself and make enrollment consumption atomic.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub



P1 — Reliability and qualification blockers

| ID   | Finding                             | Consequence                                                           |
| ---- | ----------------------------------- | --------------------------------------------------------------------- |
| F-07 | Snapshot/GC workflow is partial     | Snapshot composition and resumable transfer are unfinished.           |
| F-08 | Membership integration is partial   | The Raft ConfState driver is still convention-based.                  |
| F-09 | Fault harness is incomplete         | Real Docker/process/network failure realization is deferred.          |
| F-10 | Performance is unqualified          | No reference-hardware results exist.                                  |
| F-11 | Target applications are unqualified | No supported workload has passed the acceptance suite.                |
| F-12 | Observability is not wired          | Metrics exist as a collector, but live daemon integration is pending. |

The repository's own status report marks these areas partial or deferred.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+2



### An important scope discrepancy

There is also an architectural decision to make before investing further.

In our earlier GlusterFS discussion, the intended design was a small daemon operating over ordinary mounted ext4/XFS filesystems, coordinating declared directories between participating nodes without implementing a complete distributed filesystem.

The current repository explicitly chooses a different design: FUSE-mediated access, Raft-based metadata ordering, quorum-durable writes, and no watcher-based replication.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://github.com\&sz=32)

GitHub+1



These are fundamentally different products.

The task sheet below assumes you intend to keep the current published SyncFS architecture. If the smaller directory-replication design is still the objective, I recommend an architecture rescope before implementing the remaining work. Do not try to support both models in the same MVP.

## 4. Positive foundations worth preserving

I would not throw away the existing implementation.

There is useful work already present:

- Versioned Protobuf API and compatibility baseline.
- mTLS identity and role primitives.
- QUIC transport with bounded framing.
- WAL and snapshot primitives.
- Content-addressed chunk storage with BLAKE3 and Pebble.
- Deterministic metadata state machine.
- Raft driver, leader election and quorum-write foundations.
- Read leases, invalidation and lock-management primitives.
- A Go test and race-test baseline.

The implementation tracker records focused tests for these components, including three-node in-memory Raft tests and crash-recovery tests.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+2



I would preserve these package boundaries and avoid a major architectural rewrite unless you decide to change the product scope.

## 5. Production-readiness task sheet

13 tasks required before production approval

The following is the implementation order I recommend. The complete task sheet includes detailed instructions, dependencies, file references and exit criteria.

| Task | Priority | Objective                                                     |
| ---- | -------- | ------------------------------------------------------------- |
| T00  | P0       | Lock the product and compatibility contract                   |
| T01  | P0       | Reconcile documentation and milestone tracking                |
| T02  | P0       | Implement the actual storage-daemon runtime                   |
| T03  | P0       | Connect FUSE mutations to durable quorum writes               |
| T04  | P0       | Complete the kernel-visible filesystem and POSIX layer        |
| T05  | P0       | Close authentication and authorization gaps                   |
| T06  | P0       | Finish metadata persistence, snapshots, WAL compaction and GC |
| T07  | P0       | Complete membership and replacement-node lifecycle            |
| T08  | P0       | Build real process/network/storage fault tests                |
| T09  | P1       | Implement operational observability and runbooks              |
| T10  | P1       | Harden systemd deployment and configuration                   |
| T11  | P1       | Enforce production CI and release quality gates               |
| T12  | P0       | Qualify performance and a target application                  |
| T13  | P1       | Qualify upgrades, rollback and recovery                       |

### The five most important implementation instructions

T02 — Build the actual daemon

The daemon should become the composition root:

```
syncfs daemon
    |
    +-- Configuration
    +-- Identity / PKI
    +-- Persistent storage
    |     +-- WAL
    |     +-- Snapshot store
    |     +-- Chunk store
    |     +-- Metadata projection
    |
    +-- Raft driver
    +-- QUIC transport
    +-- Volume coordinator
    +-- Membership controller
    +-- Repair / GC
    +-- Metrics / health
```

The current metrics-only HTTP server is not this runtime.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+1



Exit criterion: Three independently running storage processes form a cluster, recover persistent state after restart, elect a leader and expose their actual cluster state.

T03 — Make durability real

The critical write path must be:

```
FUSE request
    |
    v
Client operation ID
    |
    v
Prepare immutable chunks
    |
    v
Persist chunks on storage majority
    |
    v
Commit metadata through Raft
    |
    v
Apply committed metadata
    |
    v
Return success
```

`fsync` must wait for the required durable commit. Do not substitute an in-memory acknowledgement.

Exit criterion: A successful write followed by `fsync` survives permanent loss of any one voter and a full cluster restart.

T04 — Finish the FUSE implementation

The current flat-root implementation is insufficient for applications using shared directories. The kernel must see the complete inode hierarchy, not merely an in-memory compatibility surface.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+1



Exit criterion: Real Linux mount tests successfully exercise nested directories, file operations, renames, links, permissions, locks, and durability across multiple mounts.

T05 — Close authorization gaps

Make transport authorization mandatory inside the API, not something each caller must remember to perform.

Exit criterion: Concurrent enrollment replay tests prove exactly one successful consumption, and unauthorized stream-opening attempts consistently fail.

T08 — Prove failure behavior

Do not rely exclusively on simulated Raft meshes.

Run actual processes with independent disks and network connections.

Exit criterion: The system passes leader loss, follower loss, minority partition, interrupted writes, disk-full, corruption, restart, and learner-replacement scenarios without losing acknowledged mutations.

### 6. Performance qualification

This deserves its own acceptance gate.

The repository already defines performance objectives:

| Workload               | Required target          |
| ---------------------- | ------------------------ |
| 4 KiB quorum writes    | 1,000 ops/s, p99 ≤ 25 ms |
| Create/write/rename    | 500 ops/s, p99 ≤ 30 ms   |
| Strict cache-hit reads | p99 ≤ 2 ms               |

These are currently marked UNVERIFIED. Existing Docker measurements do not include the complete QUIC/FUSE/10 GbE path.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+1



The qualification environment should have three independent NVMe-backed storage nodes and 10 GbE networking, as specified by the project.

Do not change the targets simply because the current implementation cannot reach them. Measure first, then make an explicit architectural or performance decision.

## 7. Additional repository hygiene

I found a separate problem that should be addressed early.

The engineering documentation has inconsistent completion states:

- `.spec/tasks.md` marks M3-02 and M4 tasks complete.
- `docs/spec/status.md` marks several of those tasks partial.
- `.spec/roadmap.md` describes M1–M4 as implemented.
- The task file still contains a statement that no implementation code exists.

These contradictions make it difficult to establish what has actually been delivered.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+3



Also, the systemd units need actual security directives rather than comments describing intended hardening. The CI workflow should enforce the full protobuf, FUSE and fault-test gates, not merely the existing unit and race-test checks.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://raw.githubusercontent.com\&sz=32)

GitHub+2



## 8. Complete task sheet

SyncFS Production Readiness — Implementation Plan

Markdown · 344 lines · 13 tasks · Evidence register · Exit criteria · Release gate

Created

Download the complete production-readiness task sheet

The document contains:

- Evidence register identifying the repository files behind each finding.
- Priority and dependency ordering.
- Implementation instructions for each task.
- Testable, measurable exit criteria.
- Final production-readiness checklist.
- Recommended implementation sequence.

## 9. My recommended execution strategy

I would organize development into four gates:

Gate 1

Functional correctness

T00–T07. A functioning, persistent, secure three-node filesystem.

Gate 2

Failure correctness

T08. Demonstrate that failures cannot violate acknowledged-write guarantees.

Gate 3

Operational readiness

T09–T11. Monitoring, deployment, CI, security and recovery procedures.

Gate 4

Production qualification

T12–T13. Real hardware, target application, upgrades and disaster recovery.

My final verdict: The project has a good engineering foundation, but it is currently much closer to a collection of tested distributed-storage components than a production-ready storage product.

The next objective should not be more Raft optimization, additional protocols, or more abstractions.

It should be one demonstrable result:

Two Linux application hosts can mount the same directory, perform real filesystem operations, and receive a successful durability acknowledgement only when the data is safely committed to the three-node storage cluster. That guarantee must continue to hold through node failure, restart and recovery.

One review limitation: I inspected the published source and documentation, but could not independently run the test suite because GitHub DNS access was unavailable in the execution environment. The test results above are therefore repository-reported, while the implementation gaps are based on direct source inspection.
