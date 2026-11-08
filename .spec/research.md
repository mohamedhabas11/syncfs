# Research: SyncFS Shared Directory MVP

## Goal

Define an implementable, small-cluster shared-directory service for HA
applications that need a common POSIX path with fast, coherent visibility,
encrypted replication, and safe node replacement/joining.

## Success Criteria

- An engineer can explain exactly why each acknowledged write is safe after one
  storage-node failure.
- The project has one authoritative data path instead of competing local-brick
  and replicated views.
- The specification exposes compatibility limits rather than implying that a
  watcher or NFS-like cache is strongly consistent.
- A future implementation can start with atomic, verifiable milestones.

## Context

The tracked repository contained only a two-line README. The working tree also
contained three untracked exploratory documents:

- .spec/directory-replication-daemon.md
- arch/architecture-proposal.md
- docs/directory-daemon.md

Those drafts correctly identified the need for leases, a quorum, an embedded
journal, and a small number of nodes. They also proposed watching a normal local
directory after applications wrote it. That approach conflicts with the current
goal: an observer cannot reject, serialize, quorum-commit, or invalidate a
write that the application has already completed.

No AGENTS.md or CLAUDE.md standing-order file was found. The repository is the
selected tracker because it owns the eventual client, daemon, protocol, and
delivery definition.

## Orientation

- Standing Orders: Not found.
- Milestone Table: .spec/state.md.
- Open Spec Tasks: .spec/tasks.md.
- Dependency Map: .spec/state.md.

## Current Crawl Loop

- Context Target: SyncFS filesystem/data-path architecture.
- Domain: Product logic and persistent data.
- Scope Boundary: The design intentionally does not select a datacentre storage
  platform, an NFS/SMB gateway, WAN replication, or a Kubernetes integration.

## Key Findings

- Linux FUSE provides the necessary kernel-to-userspace interception point and
  explicit cache invalidation mechanisms. The strict MVP must keep cache
  authority in the mount process so a lost connection cannot keep serving a
  stale kernel page cache.
- Raft is appropriate for a small, strongly ordered metadata namespace and
  membership state. It does not store file bytes by itself, so data-preparation
  and committed-metadata ordering must be part of the SyncFS protocol.
- QUIC provides multiplexed streams and TLS 1.3 protection, which keeps a large
  chunk transfer from blocking control, lease, or Raft traffic.
- A Go implementation is consistent with the existing drafts and has mature
  FUSE, Raft, QUIC, and embedded-LSM building blocks. Current quic-go releases
  require a modern Go toolchain; the MVP baseline is Go 1.25 or newer.
- A content-addressed chunk/manifest layout makes small range writes avoid
  copying a full large file, while append-only packs avoid a local filesystem
  directory full of tiny chunk files.

## Interface Ownership

- FUSE/POSIX adapter: internal/fusefs, exposed only through syncfs mount.
- Cluster metadata, locks, and ordering: volume Raft state machine.
- Immutable file content: internal/chunkstore, referenced only by committed
  manifests.
- Node identity and enrollment: internal/auth and control-plane commands.
- Network framing: api/v1 generated Protobuf code and internal/transport.

## Affected Files / Modules

- README.md: Product boundary and entry points.
- docs/architecture.md: Human-readable topology and request flows.
- docs/compatibility.md: POSIX contract and explicit exclusions.
- docs/operating-model.md: Deployment and air-gapped operations.
- .spec/technical_spec.md: Normative implementation contract.
- .spec/tasks.md: Ordered delivery plan.

## Constraints & Assumptions

- Linux-only hosts may run a FUSE mount process. Without this assumption, the
  requested consistency contract is not technically achievable on direct native
  paths.
- The first production topology has three storage voters on a low-latency LAN.
- Each storage node has a private local ext4/XFS filesystem and correctly honors
  fsync; local disk write-cache protection is an operator requirement.
- Applications access only the mounted path, never a backend data directory.
- Encryption in transit is mandatory. At-rest encryption is provided below the
  daemon with LUKS/dm-crypt in the MVP.

## Risks

- FUSE correctness, especially cache behavior and file locks, is a core product
  risk and must be exercised on real Linux kernels under fault injection.
- Quorum durability raises tail latency; a reference hardware benchmark is a
  release gate, not a marketing claim.
- The first compatibility target may rely on an unsupported POSIX corner case.
  The compatibility matrix and qualification suite must stay ahead of any
  support statement.
- Implementing data replication outside Raft requires strict crash-order tests;
  an incorrect data/metadata ordering could produce committed missing chunks.

## Evidence and Technology Sources

- [Linux FUSE documentation](https://docs.kernel.org/filesystems/fuse/index.html)
  documents the kernel/userspace interface and cache model.
- [Linux FUSE cache documentation](https://docs.kernel.org/filesystems/fuse/fuse-caches.html)
  documents attribute and explicit inode/dentry invalidation behavior.
- [go-fuse](https://github.com/hanwen/go-fuse) provides Go bindings for the
  FUSE protocol, including lock forwarding and cache-control hooks.
- [etcd Raft](https://pkg.go.dev/go.etcd.io/etcd/raft/v3) is a production Raft
  core that intentionally leaves disk and network I/O to the embedding system,
  which fits SyncFS's QUIC/WAL requirements.
- [quic-go](https://github.com/quic-go/quic-go) is a Go implementation of QUIC;
  its current releases track modern Go versions.
- [RFC 9001](https://www.rfc-editor.org/rfc/rfc9001.html) specifies TLS 1.3
  protection for QUIC.
- [Pebble](https://github.com/cockroachdb/pebble) is a production-used Go LSM
  suitable for local ordered metadata/index state.

## Verification Candidates

- git diff --check: Detect whitespace errors in the foundation patch.
- rg -n "Superseded exploratory draft|FUSE-mediated|quorum" README.md docs .spec:
  Ensure the active model does not silently coexist with the retired one.
- go test ./...: Baseline unit/integration suite once go.mod exists.
- go test -race ./...: Detect concurrency defects in client, Raft, and cache
  code once the implementation exists.
- Linux-only three-node fault test: Verify writes, locks, failover, learner
  promotion, and partition behavior against the specified invariants.

## Strategy Proposal

Build a direct FUSE client and a small Raft-backed volume service, not a sync
daemon. Start with one strict volume on exactly three voters. Persist metadata
commands in Raft and file content in immutable replicated chunks. Require
quorum chunk durability before a manifest/metadata command commits. Keep the
strict mount's cache in userspace and invalidate it upon lease loss. Add node
joining only after WAL, snapshot, chunk verification, and failover behavior are
proven.
