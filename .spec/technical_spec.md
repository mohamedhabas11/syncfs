# Technical Spec: SyncFS MVP

Status: Foundation contract
Date: 2026-11-08
Tracker repository: /Users/mohamed/syncfs

## 1. Problem statement

HA applications often need one shared directory across two or more application
hosts. NFS/SMB-backed mounts commonly make that possible, but their cache,
locking, failover, and small-operation behavior is difficult to reason about for
busy shared homes. A background file replicator has the opposite problem: it
does not see a mutation until after a local application has already succeeded.

SyncFS supplies a small, strongly consistent POSIX shared-directory service for
this workload class. It prioritizes metadata operations, small writes,
predictable failure behavior, and operational clarity over arbitrary capacity
growth or broad filesystem compatibility.

## 2. MVP scope and non-goals

### In scope

- Linux application hosts using a SyncFS FUSE mount.
- A three-voter storage cluster on a low-latency LAN.
- One strict, replicated volume per initial cluster; the architecture supports
  future additional volume groups without changing the wire model.
- Regular files, directories, symlinks, hard links, atomic namespace changes,
  durable fsync, and cluster-coherent advisory locks.
- Quorum-durable writes, leader failover, encrypted peer/client traffic, and
  learner-first replacement node admission.
- Ordinary local ext4/XFS storage owned exclusively by the daemon.

### Explicit non-goals

- CephFS/S3-class capacity, erasure coding, arbitrary sharding, or a global
  data-lake namespace.
- Serving existing NFS or SMB clients.
- Directly mounting or modifying a replica's backing directory.
- WAN/multi-region deployments, disconnected writes, and conflict resolution.
- Strict writable shared mmap, O_DIRECT, device files, sockets, or FIFOs.
- Two-node strong operation without an independent third voter.
- Application-level at-rest encryption or external KMS integration in the MVP.

## 3. Acceptance criteria

The MVP is complete only when all conditions below are demonstrably true.

1. A three-node volume acknowledges a mutation only after its metadata command
   is committed by a Raft majority and all newly referenced content is durable
   on a storage majority.
2. Losing one storage node does not lose an acknowledged mutation or stop a
   healthy two-node majority from accepting new writes.
3. A partition minority cannot acknowledge mutations, grant locks, or serve
   newly uncached strict reads.
4. Two connected mounts observe successful mutations in one order; stale cached
   data is never knowingly served after a lease is revoked, expires, or loses
   its control connection.
5. rename, O_CREAT|O_EXCL, flock, POSIX range locks, fsync, and fsyncdir pass
   the integration contract in docs/compatibility.md.
6. Every peer, client, snapshot, and chunk transfer is mutually authenticated
   and encrypted with TLS 1.3 over QUIC.
7. A replacement node can join as learner, verify a snapshot and its chunks,
   catch up to the committed index, become voter through a safe membership
   change, and replace a failed voter without reducing quorum first.
8. Fault tests cover leader loss, follower loss, network minority, interrupted
   chunk upload, daemon restart at each persistence boundary, disk-full, corrupt
   chunk, client reconnect, and learner promotion.

## 4. Normative invariants

The words MUST, MUST NOT, and SHOULD are normative in this section.

1. **One authority:** Applications MUST access a volume through syncfs mount.
   A daemon's local data directory MUST NOT be exported or writable by users.
2. **Committed visibility:** A namespace or inode version is visible only after
   its command is committed in that volume's Raft log and applied locally.
3. **Payload before reference:** A metadata command MUST NOT be acknowledged if
   a newly referenced chunk is not locally durable on the leader and a majority
   of current storage voters.
4. **Apply only with payload:** A replica MUST NOT apply a committed command
   that references an absent or hash-invalid chunk. It MUST fetch and verify the
   chunk first, and it MUST NOT campaign for leader while such a committed entry
   remains unapplied.
5. **No quorum, no mutation:** A node without a current majority-confirmed
   leader MUST NOT acknowledge writes, lock grants, lease grants, membership
   changes, or administrative mutations.
6. **Strict cache safety:** A strict mount MUST invalidate its userspace and
   kernel-visible cached state before its lease expires or when its control
   connection fails. It MUST return an availability error rather than knowingly
   serve an expired cached value.
7. **Idempotency:** Every client mutation MUST carry a stable 128-bit operation
   ID. The leader MUST durably retain a bounded result record so retrying the
   same operation ID never creates a second mutation.
8. **Membership sequencing:** At most one voter membership change may be in
   progress per cluster. A learner MUST NOT count toward a data or Raft quorum.
9. **Fail closed:** Hash mismatch, WAL corruption, certificate mismatch, format
   mismatch, or an unresolvable invariant violation MUST make the affected node
   non-voting and visible to operators; it MUST NOT silently self-heal by
   discarding committed state.
10. **No hidden downgrade:** A client or volume configured as strict MUST NOT
    transparently fall back to asynchronous replication or stale reads.

## 5. Architecture overview

### 5.1 Roles and topology

| Role | Count in MVP | Persistent data | May serve app I/O | Votes |
| --- | --- | --- | --- | --- |
| Storage voter | Exactly 3 | Raft WAL, metadata projection, chunks, snapshots | Yes | Yes |
| Storage learner | 0–1 during membership work | Same as voter while catching up | No | No |
| Client-only mount host | Any | Local request journal/cache only | Yes, through FUSE | No |
| Administrator | Any approved operator host | Credentials/bundles only | Control-plane calls | No |

The control plane owns cluster identity, pending enrollment records, and volume
specifications. A volume owns its own Raft group and state machine. The MVP
creates one volume group with all three storage voters; later volumes may use the
same model without sharing their ordered mutation log.

### 5.2 Required components

| Layer | Component | Responsibility |
| --- | --- | --- |
| Kernel boundary | internal/fusefs | Translate Linux FUSE requests and errors; forward locks; use strict cache policy. |
| Client protocol | internal/client | Sessions, leader routing, operation IDs, request journal, read leases, retries. |
| Transport | internal/transport | QUIC sessions, mTLS, ALPN, stream framing, flow control, backpressure. |
| Consensus | internal/raftstore | Raft Ready loop, durable WAL, snapshots, membership changes, ReadIndex. |
| Metadata | internal/metadata | Inodes, dentries, permissions, manifests, locks, command application. |
| Content | internal/chunkstore | Staging, immutable chunk packs, checksums, index, GC, scrub, transfer. |
| Volume coordination | internal/volume | Quorum data preparation, lease revocation, Raft proposals, repair eligibility. |
| Security/admin | internal/auth, internal/admin | PKI, roles, enrollment, operator commands, audit. |

## 6. Component selection

| Need | Selected baseline | Rationale |
| --- | --- | --- |
| Language/runtime | Go 1.25+ on Linux | Matches existing design direction; modern Go is required by current QUIC dependencies. |
| FUSE adapter | github.com/hanwen/go-fuse/v2 | Go-native FUSE protocol coverage, lock forwarding, explicit cache controls, and direct Linux focus. |
| Consensus core | go.etcd.io/etcd/raft/v3 | Mature deterministic Raft core; SyncFS retains control over persistence and encrypted transport. |
| Transport | github.com/quic-go/quic-go plus Go TLS | Multiplexed streams with TLS 1.3 protection and no TCP head-of-line coupling. |
| Metadata/index projection | github.com/cockroachdb/pebble | Embedded ordered KV for replicated metadata and chunk indexes; not an external authority. |
| Wire encoding | Protobuf, source in api/v1 | Versioned schemas, bounded messages, generated Go code, and testable compatibility. |
| Chunk digest | BLAKE3 | Fast content addressing and corruption detection; QUIC/TLS remains the transport integrity/authentication layer. |

Exact module versions, license review, SBOM generation, and Go toolchain pinning are
part of task M1-01. No code may depend on a moving branch or unpinned module.

## 7. Data models and persistent format

### 7.1 Logical identifiers

~~~go
type ClusterID [16]byte
type VolumeID [16]byte
type NodeID [16]byte
type InodeID [16]byte
type SessionID [16]byte
type OperationID [16]byte
type ChunkID [32]byte // BLAKE3-256

type Version struct {
    RaftIndex uint64
    Revision  uint32
}
~~~

RaftIndex establishes the total order of committed state. Revision permits more
than one logical object update inside a single command without inventing a
second global clock.

### 7.2 Volume specification

~~~go
type VolumeSpec struct {
    ID             VolumeID
    Name           string
    FormatVersion  uint32
    Consistency    string // MVP: strict
    VoterCount     uint8  // MVP: exactly 3
    ChunkSizeBytes uint32 // MVP: 65536
    Durability     string // MVP: quorum
    QuotaBytes     uint64
    MaxFileBytes   uint64
    ACLPolicy      string
}
~~~

The volume specification is cluster-committed state. FormatVersion,
ChunkSizeBytes, and Consistency are immutable after first write. Changes to
limits or access policy are versioned administrative commands.

### 7.3 Inodes, dentries, and manifests

~~~go
type Inode struct {
    ID          InodeID
    Kind        InodeKind // file, directory, symlink
    Version     Version
    Mode        uint32
    UID         uint32
    GID         uint32
    LinkCount   uint32
    Size        uint64
    AtimeNs     int64
    MtimeNs     int64
    CtimeNs     int64
    ManifestID  ChunkID // file only
    SymlinkData []byte  // symlink only, bounded by Linux PATH_MAX
    Xattrs      map[string][]byte
}

type Extent struct {
    FileOffset  uint64
    Length      uint32
    Chunk       ChunkID
    ChunkOffset uint32
}

type FileManifest struct {
    Version Version
    Size    uint64
    Extents []Extent // sorted, non-overlapping; gaps are sparse holes
}
~~~

Dentries are keyed by parent inode ID and normalized byte name and resolve to an
inode ID plus the parent generation at which they were observed. Names are
opaque Linux byte strings excluding slash and NUL; SyncFS does not impose an
application-specific Unicode normalization rule.

### 7.4 Metadata command

~~~go
type Command struct {
    OperationID      OperationID
    Principal        Principal
    Preconditions    []ExpectedVersion
    Kind             CommandKind
    Payload          []byte // versioned protobuf oneof
    PreparedChunks   []PreparedChunkReceipt
    RequiredLeaseAcks []LeaseID
}
~~~

CommandKind includes Create, WriteRanges, Truncate, Link, Unlink, Rename,
SetAttr, SetXattr, SetACL, LockGrant, LockRelease, VolumeConfig, and
MembershipPlan. An apply either changes every listed logical record or changes
none. Preconditions prevent accidental lost updates.

### 7.5 Local layout

~~~text
<data-dir>/
  identity/                 certificates, local node ID, trust bundles
  groups/<group-id>/wal/    length-delimited Raft records and hard state
  groups/<group-id>/snap/   checksummed metadata checkpoints
  volumes/<volume-id>/
    pebble/                 local metadata plus chunk index projection
    packs/                  immutable append-only chunk pack segments
    staging/                uploaded but uncommitted chunks
    snapshots/              incoming/outgoing chunk manifests
    quarantine/             immutable forensic evidence
~~~

All paths are daemon-private. WAL records, snapshots, packs, and Pebble state
include a format version, length bounds, and checksums. Recovery never accepts a
truncated valid-prefix record as a committed command unless the Raft hard state
proves it was persisted.

## 8. Protocol and transaction lifecycle

### 8.1 Connection rules

- The ALPN value is syncfs/1.
- Every QUIC endpoint presents a certificate chained to the cluster CA.
- Certificates contain the expected cluster identity and node/client role.
- Every request includes cluster ID, protocol version, authenticated principal,
  and a bounded request ID.
- 0-RTT application data is disabled. A reconnect must complete authentication
  before reads, lease renewal, or mutations.
- Streams are classed as raft, control, client, chunk, and snapshot; the
  transport reserves capacity for Raft and lease revocation traffic.
- Protobuf length prefixes have strict maximum sizes. Chunk bytes travel on
  dedicated chunk streams; they are never inserted into a Raft entry.

### 8.2 Quorum write protocol

For a write that changes file content, the leader performs the following exact
sequence:

1. Validate volume state, caller authorization, path/inode versions, session,
   and any required lock/lease.
2. Allocate or reuse the stable OperationID. A duplicate returns its retained
   result without allocating new state.
3. Convert only affected file ranges to 64 KiB copy-on-write chunks and create
   a candidate immutable manifest. A write spanning an existing chunk reads and
   rewrites that one chunk; it does not copy the whole file.
4. Store each missing chunk locally in staging, verify the BLAKE3 digest, force
   it durable, and stream it to enough current voters to form a majority.
5. Receive a durable receipt from each required voter. A receipt identifies the
   peer, chunk hash, local store generation, and verified byte count.
6. Revoke conflicting read/directory leases. A live lease holder must
   invalidate and acknowledge; a dead session must expire before the command
   can proceed.
7. Propose exactly one WriteRanges command containing the old versions, new
   manifest ID, receipts, and operation ID to the volume Raft group.
8. Persist the Raft entry and hard state before sending Raft acknowledgements.
   Wait for majority commit, fetch any missing chunks before local application,
   atomically apply the metadata batch, and durably record the operation result.
9. Send invalidation notifications and return success to the FUSE client.

Metadata-only operations skip chunk preparation but retain the same validation,
lease-revocation, Raft-commit, atomic-apply, and idempotency rules. Rename is
one command even when it changes two parent directories.

An interrupted request has three legal outcomes: known committed, known rejected,
or unknown. The client asks GetOperation(OperationID) before retrying. If a
leader cannot prove the outcome before the request deadline, FUSE receives an
I/O error and the operator can inspect the operation ID; the client MUST NOT
manufacture a second mutation.

### 8.3 Read, cache, and lock protocol

The strict mount has two cache layers:

- A userspace cache in syncfs mount, keyed by inode and version and guarded by
  a server-issued lease.
- Minimal kernel FUSE caching with zero/very short metadata TTL and direct I/O
  for mutable file data in the initial release. This prevents a paused or
  disconnected mount process from allowing the kernel to serve an expired page.

A leader grants a ReadLease only to a healthy connected client session and
records the inode/directory generation. Before a conflicting command commits,
it sends an invalidation for every relevant lease. The mount discards matching
userspace entries, sends FUSE invalidation where applicable, and acknowledges.
If a control connection drops, the mount invalidates all leases before serving
another request. Read-only cached chunks may be reused only after revalidating
the inode generation.

Fresh reads use a Raft ReadIndex barrier or a valid leader-confirmed lease.
Followers may proxy requests, but do not independently invent cache authority.
The implementation must test host suspension and mount-process death; strict
correctness wins over an unsafe kernel page-cache optimization.

flock and POSIX range locks are state-machine records keyed by inode, owner
session, process lock owner, range, and lock mode. Blocking locks wait on the
leader; nonblocking locks return EAGAIN. Session expiry releases its locks in
Raft order and emits invalidations.

### 8.4 Raft persistence and leadership

The Raft driver uses Ready/Advance semantics:

1. Persist WAL entries and hard state with fdatasync before transmitting the
   associated Raft messages.
2. Persist snapshots before reporting them applied.
3. Deliver committed entries to the state machine in index order only after all
   referenced chunks are locally verified.
4. Advance Raft only after persistence and apply obligations are complete.

A node with a WAL, snapshot, metadata, or chunk gap is CATCHING_UP and cannot
campaign or vote. Raft's committed term/index is the fencing token; no external
lease manager or writable direct brick exists to fence.

### 8.5 Snapshot, repair, and garbage collection

A snapshot consists of a checksummed metadata checkpoint, the applied Raft
index/term, and a manifest of reachable chunks. The receiver installs it into a
new directory, verifies metadata and chunk hashes, atomically activates it, then
replays subsequent entries. A learner follows this same path.

Background anti-entropy compares snapshot/chunk manifests and repairs missing
objects from a healthy voter. A chunk is collectible only when no retained
metadata snapshot, committed manifest, in-flight operation, or valid client
read lease can reference it. GC is mark-and-sweep with a persisted generation;
it never deletes a chunk based solely on a local reference count after crash.

## 9. Security model

| Area | MVP requirement |
| --- | --- |
| Transport | TLS 1.3 over QUIC with mutual certificate authentication for every connection. |
| Trust | Private, offline-capable cluster CA; cluster ID and role are bound to certificate identity. |
| Enrollment | Admin pre-authorizes a node fingerprint and one-time nonce in committed state before learner contact. |
| Authorization | Volume-scoped admin/operator/client roles plus POSIX uid/gid/mode checks at the metadata leader. |
| Replay | 0-RTT disabled; stable operation IDs deduplicate retries. |
| Secrets | Private keys and enrollment bundles have restrictive file permissions, are never logged, and are redacted from diagnostics. |
| At rest | LUKS/dm-crypt below daemon storage where required; application-layer encryption/KMS is out of scope. |
| Audit | Membership, certificate, volume configuration, and forced recovery actions produce immutable structured audit records. |

The storage daemon trusts the authenticated mount host to report the FUSE
kernel's uid/gid credentials. A compromised root user on that host can already
impersonate local users; that host is therefore inside the volume's trust
boundary. Server-side POSIX checks remain mandatory to prevent ordinary client
processes from bypassing permission policy.

## 10. Failure and recovery behavior

| Scenario | Required behavior |
| --- | --- |
| Leader loses quorum | It steps down or cannot commit; outstanding mutations become known/unknown via operation lookup, never successful without quorum. |
| Minority partition | No mutations, leases, locks, promotions, or uncached strict reads. Existing FUSE cache is invalidated. |
| Leader crash after chunk receipts, before command commit | Chunks remain unreachable garbage; a new leader can reuse only after safe operation lookup/precondition evaluation. |
| Crash after Raft commit, before local apply | On recovery, verify chunks, apply exactly once, and return retained operation result. |
| Chunk corrupt/missing on follower | Refuse apply/candidacy, fetch from healthy peer, verify digest, then resume catch-up. |
| Corrupt WAL/snapshot | Quarantine local state, remain non-voting, and rebuild from a healthy snapshot; never truncate committed state automatically. |
| Client disconnect during mutation | Query stable operation ID after reconnect. If unresolved by deadline, report an error rather than guessing. |
| Disk fills | Reject writes that cannot obtain durable chunk receipts from a majority; keep existing committed data readable. |
| Learner interruption | Resume verified snapshot/chunk transfer; learner remains non-voting. |

## 11. Performance and resource contract

SyncFS optimizes for bounded small-operation latency, not local-filesystem
latency. A quorum-durable write necessarily waits for network and remote durable
storage. The MVP uses the following mechanisms:

- fixed 64 KiB copy-on-write extents to avoid full-file copies for small range
  writes;
- immutable append-only pack segments and Pebble indexes to reduce local small
  file and directory metadata amplification;
- short, configurable group commit bounded to 0–5 ms (default target: 2 ms);
- separate, prioritized QUIC streams for Raft/leases versus bulk chunks;
- pipelined follower chunk preparation and Raft replication; and
- userspace versioned read cache rather than unsafe long-lived kernel caching.

Reference qualification hardware is three storage nodes with NVMe that honors
flushes and a 10 GbE low-latency network. Before claiming a release candidate,
the benchmark suite must show, on that named profile:

- 4 KiB quorum write plus fsync at 1,000 operations/second with p99 at or below
  25 ms;
- create/write/rename of a 4 KiB file at 500 operations/second with p99 at or
  below 30 ms;
- cache-hit strict reads with p99 at or below 2 ms; and
- no correctness failure, unbounded queue growth, or replica lag under the
  sustained run.

These are qualification gates, not universal production promises. The benchmark
report records CPU model, kernel, storage flush behavior, network, volume size,
and exact binary/configuration.

## 12. Operations and rollout

### 12.1 Bootstrap and join

1. Generate an offline cluster CA and three scoped node certificates.
2. Bootstrap the first node with a new ClusterID; enroll the second and third
   nodes as learners and promote each only after validation.
3. Create the strict volume only after three healthy voters exist.
4. Enroll application mount hosts with client certificates, mount the volume,
   then start the dependent service.
5. Add a replacement node as learner, scrub/catch it up, use Raft joint
   consensus to promote it, drain the departing voter, and remove it last.

Only one voter membership change may run at a time. The CLI must refuse unsafe
orders rather than relying on operator memory.

### 12.2 Application cutover

The initial importer is offline only:

1. Quiesce the application and verify no writer holds the old shared directory.
2. Create a new SyncFS volume, import/copy the source through an administrative
   ingest path, and verify a tree manifest/checksum report.
3. Mount the new volume at the application's required path.
4. Start one application node, then the remaining HA nodes after successful
   functional checks.

Rollback after accepting new writes is a data migration, not a mount toggle.
The runbook must preserve the SyncFS volume and reconcile/copy data back before
reusing an old NFS/SMB path.

### 12.3 Observability

Expose structured logs and metrics for leader/quorum status, committed/applied
indexes, follower/learner lag, lease invalidation latency, lock wait time,
operation outcome ambiguity, per-operation latency, disk headroom, chunk
integrity/repair, certificate expiry, and all admin/membership events.

## 13. Verification plan

| Layer | Evidence |
| --- | --- |
| Codec and data structures | Unit tests, fuzz tests for framing/name/path/manifest decoding. |
| WAL and chunk store | Crash-point tests that kill a process after every persistence boundary; reopen/verify invariant. |
| Metadata state machine | Deterministic tests for every command, precondition, permission, lock, and inode lifetime transition. |
| Raft integration | In-memory deterministic transport plus real QUIC three-node tests; exercise leader transfer and compaction. |
| FUSE adapter | Linux integration suite for POSIX profile, errors, caching, lock forwarding, and client crash. |
| Fault tolerance | Network partitions, disk-full, corrupt chunks, node restart, delayed traffic, duplicate RPCs, and learner interruption. |
| Security | Certificate/role/cluster-ID rejection tests, replay tests, secret redaction tests, dependency/SBOM audit. |
| Performance | Reproducible benchmark harness and reference hardware report described in section 11. |
| Application | At least one target HA service's shared-home suite on two application hosts. |

Minimum commands after code exists:

~~~text
go test ./...
go test -race ./...
go test -run TestThreeNode -count=1 ./internal/...
go test -run TestFUSE -count=1 ./internal/fusefs/...
go test -run TestFault -count=1 ./internal/testkit/...
~~~

The project must run FUSE and network-fault tests on Linux CI. macOS developer
tests are not a substitute for the production kernel boundary.

## 14. Open questions

1. Which concrete application/version is the first qualification target, and
   which filesystem calls does its HA suite require beyond the current profile?
2. Must the first production deployment integrate with an existing enterprise
   CA, or is the offline built-in CA workflow sufficient?
3. Is host-level LUKS/dm-crypt an acceptable at-rest encryption boundary for the
   MVP, or is a key-manager-backed application encryption layer required?
4. What quota model is needed first: only volume quota, or per-directory/user
  quota? The MVP data model reserves limits but does not implement policy yet.
