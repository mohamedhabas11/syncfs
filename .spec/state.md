# Spec State: SyncFS Shared Directory MVP

## Current Stage

Implemented through M4 (see `docs/spec/status.md` for evidence-linked
milestone state). Foundation runs on Go 1.26; the reduced milestone set is
M5 (deferred, ADR-0001) + release prep.

## Tracker Boundary

/Users/mohamed/syncfs is the single durable tracker repository.

## Milestone Table

| Milestone | Status | Owner / Lane | Dependencies | Verification |
| --- | --- | --- | --- | --- |
| Phase 0 foundation | done | research/00 + D-001 | Repo orientation + research | docs audit |
| M1 (toolchain+protocols+auth+QUIC) | done | tasks M1-01..04 | Phase 0 | go test/buf/fleet (evidence in git log) |
| M2 (durable replicated core) | done | tasks M2-01..06 | M1 | three-node mesh + unit suites |
| M3 (FUSE mount + compatibility + client failure) | done | tasks M3-01..03 | M2 | flagfleet smoke + compat trace |
| M4 (HA/operations/alert) | done | tasks M4-01..04 | M2+M3 | GC/membership/CLI/fault suites |
| M5 (workload qualification) | deferred | ADR-0001 | reference hardware + target app | human + workloads suite |

## Dependency Map

| Interface / Contract | Owning Repo / Service | Consumers | Source of Truth |
| --- | --- | --- | --- |
| POSIX mount compatibility profile | syncfs / internal/fusefs | Application hosts | docs/compatibility.md |
| Volume consistency and durable-write invariant | syncfs / internal/volume | FUSE mount, storage daemon, operators | .spec/technical_spec.md |
| Peer/client wire protocol | syncfs / api/v1 | Storage nodes and mounts | .spec/technical_spec.md |
| Membership and enrollment workflow | syncfs / internal/auth, internal/raftstore | Operators and storage nodes | docs/operating-model.md |
| Persistent local format | syncfs / internal/raftstore, internal/chunkstore | Storage nodes | .spec/technical_spec.md |

## Active Deliverables

- Research: .spec/research.md + research/07 (fork reconciliation)
- Technical Spec: .spec/technical_spec.md
- Tasks: .spec/tasks.md
- Roadmap: .spec/roadmap.md
- Status: docs/spec/status.md, PROGRESS.md

## Current Crawl Target

- Target: syncfs repository root
- Domain: Distributed filesystem architecture (implemented)
- Tracker Repo: /Users/mohamed/syncfs

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

(Defaults adopted; see `.spec/technical_spec.md` §14.)

- **Target qualification workload**: settled truthfully as "unspecified" pending
  HUMAN_TODO (no support claims).
- **Identity integration**: offline CA is sufficient for MVP; enterprise PKI is
  a post-MVP adapter.
- **At-rest encryption**: LUKS/dm-crypt is the accepted MVP boundary.

## Verification Status

- git diff --check: Passed
- markdown/mkdocs links: Passed (`mkdocs build --strict` green)
- go build ./... · go vet ./... · go test -race ./... : all green (18 pkgs)

## Handoff Notes

The earlier directory-daemon documents are archived historical (D-001).
Wiki history lives in research/ + .spec/research.md. Next work: FUSE-backed
version of the volume/coordinator wiring, then M5 qualification when a target
workload and reference hardware exist.
