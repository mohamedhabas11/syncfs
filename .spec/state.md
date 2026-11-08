# Spec State: SyncFS Shared Directory MVP

## Current Stage

Spec + Tasks complete; implementation has not started.

## Tracker Boundary

/Users/mohamed/syncfs is the single durable tracker repository. It owns the
mount contract, the storage protocol, the future binaries, and the readiness
definition. There are no implementation repositories yet.

## Milestone Table

| Milestone | Status | Owner / Lane | Dependencies | Verification |
| --- | --- | --- | --- | --- |
| Foundation architecture and MVP contract | done | Codex | Repository orientation and design research | Markdown/link review and git diff --check |
| Repository/toolchain baseline | ready | Codex | Go 1.25+ toolchain | go test ./... |
| Strict single-volume replicated filesystem | ready | Codex | Protocol, WAL, metadata, chunk store, FUSE | Three-node integration test |
| HA, enrollment, and recovery | blocked | Codex | Strict-volume milestone | Partition, failover, learner promotion tests |
| Workload qualification | blocked | Human + Codex | HA milestone and target service deployment | Application HA suite and benchmark evidence |

## Dependency Map

| Interface / Contract | Owning Repo / Service | Consumers | Source of Truth |
| --- | --- | --- | --- |
| POSIX mount compatibility profile | syncfs / internal/fusefs | Application hosts | docs/compatibility.md |
| Volume consistency and durable-write invariant | syncfs / internal/volume | FUSE mount, storage daemon, operators | .spec/technical_spec.md |
| Peer/client wire protocol | syncfs / api/v1 | Storage nodes and mounts | .spec/technical_spec.md |
| Membership and enrollment workflow | syncfs / internal/auth, internal/raftstore | Operators and storage nodes | docs/operating-model.md |
| Persistent local format | syncfs / internal/raftstore, internal/chunkstore | Storage nodes | .spec/technical_spec.md |

## Active Deliverables

- Research: .spec/research.md
- Technical Spec: .spec/technical_spec.md
- Tasks: .spec/tasks.md
- Roadmap: .spec/roadmap.md

## Current Crawl Target

- Target: syncfs repository root
- Domain: Distributed filesystem architecture
- Tracker Repo: /Users/mohamed/syncfs
- Next Reroute: First implementation task, M1-01

## Decisions

- **FUSE-mediated data path**: A post-write watcher cannot supply coordinated
  POSIX write, lock, or cache semantics. SyncFS must be in the I/O path.
- **Three storage voters**: Strict writes require a real majority; two nodes
  without an independent witness cannot safely distinguish failure from a
  partition.
- **Quorum-durable chunks before metadata commit**: Prevents a committed
  namespace state from referring to data held only by a failed leader.
- **No NFS/SMB server in the MVP**: A gateway creates another cache/lock
  consistency surface before the native client is proven.
- **Encrypted transport; operator-managed disk encryption**: mTLS QUIC is in
  scope now. At-rest key management is deliberately not invented in v1.

## Open Questions

- **Target qualification workload**: Which exact application/version should be
  the first compatibility target? This affects the integration suite, not the
  foundational safety model.
- **Identity integration**: Should production installs use only the built-in
  offline CA workflow, or also an existing enterprise PKI? This affects CLI and
  certificate adapter scope.
- **At-rest encryption**: Is LUKS/dm-crypt sufficient for the first deployment,
  or is application-layer/key-manager integration required? The answer changes
  the security milestone.

## Verification Status

- git diff --check: Passed - checked the tracked diff and every new document with
  no-index whitespace validation.
- Markdown link audit: Passed - all local Markdown targets resolve and fenced
  block delimiters are balanced.
- go test ./...: Blocked - no Go module or implementation exists; the local
  Go 1.15.6 toolchain is below the planned Go 1.26 baseline.
- Phase-0 closure (2026-11-08): Design fork closed by D-001 (research/07); ARCHIVE banners applied to camp-B files; technical corrections applied per research/00-synthesis-phase0-closure.md. Phase 0 = "foundation complete, implementation not started"; remaining gate boxes (Go 1.26 pins, M1-01) move to M1 exit criteria.

## Handoff Notes

The earlier untracked directory-daemon documents have been explicitly marked as
historical. They use a watcher-after-write model and are not safe to implement
for this shared-directory requirement. Start implementation from
.spec/tasks.md, keeping the durable-write invariant intact.
