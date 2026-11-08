# SyncFS

SyncFS is a Linux-only, replicated POSIX shared-directory service for small,
high-availability deployments. It targets the shared-home and shared-directory
workloads behind services such as repository managers, artifact servers, Jira,
and Confluence—not datacentre-scale capacity, object storage, or a general
distributed filesystem.

It is intentionally a **silver-tier** system:

- three storage nodes on a low-latency LAN form one strongly consistent volume;
- application hosts mount that volume directly through a SyncFS FUSE client;
- acknowledged mutations are durably replicated to a quorum before success is
  returned;
- replication, control traffic, and joining use mutually authenticated TLS 1.3
  over QUIC; and
- a replacement or expansion node joins as a non-voting learner, verifies a
  snapshot, and is promoted only after it is caught up.

SyncFS is not an NFS or SMB proxy. Those protocols are deliberately outside the
MVP data path: their client cache and lock semantics would reintroduce the
consistency problems this project exists to avoid. A FUSE mount is a deployment
requirement, not an implementation detail.

## Status

Pre-1.0, not production ready. Foundation (M1–M4) is implemented and green
(`go build ./...`, `go vet ./...`, `go test -race ./...` — 18 packages);
qualification (M5) is deferred pending reference hardware (ADR-0001).
Per-milestone state with evidence: [`docs/spec/status.md`](docs/spec/status.md).

The durable engineering record sits in [`.spec/`](.spec/):

| Document | Purpose |
| --- | --- |
| [`.spec/technical_spec.md`](.spec/technical_spec.md) | Normative MVP contract, storage layout, protocol, failure model, and package boundaries. |
| [`.spec/tasks.md`](.spec/tasks.md) | Atomic implementation tasks and verification gates (current tick state). |
| [`.spec/roadmap.md`](.spec/roadmap.md) | Milestones beyond the first usable cluster. |
| [`docs/architecture.md`](docs/architecture.md) | Engineer-facing architecture overview and request flows. |
| [`docs/posix-compat.md`](docs/posix-compat.md) | POSIX surface matrix (operation \| status \| test). |
| [`docs/operating-model.md`](docs/operating-model.md) | Deployment, security, joining, and operational model. |

## Core contract

| Concern | MVP decision |
| --- | --- |
| Availability unit | Three storage voters; one failure may be tolerated without losing write availability. |
| Write consistency | A successful mutation is committed in the volume Raft log and its referenced chunks are durable on a majority. |
| Read consistency | Coherent client cache leases; writes revoke affected leases before becoming visible. |
| Backend | Private local ext4/XFS storage on each storage node. Direct access to this backing data is forbidden. |
| Small writes | Copy-on-write 64 KiB content chunks, pipelined QUIC streams, and short bounded group commits. |
| Node admission | Offline-capable PKI plus a one-time enrollment bundle; learner first, voter only after verified catch-up. |
| Encryption | Mutual TLS 1.3 over QUIC in transit. At-rest encryption is supplied by LUKS or equivalent in the MVP. |

The unavoidable trade-off is honest: a quorum-durable write cannot have local
disk latency. SyncFS optimizes the networked commit path; it does not pretend to
make a replicated `fsync` as cheap as a local one.

## Scope boundary

SyncFS is designed for a few shared, high-value directories with many metadata
operations and small files. It does not attempt to replace CephFS, S3, or a
capacity-oriented GlusterFS deployment. The initial implementation excludes WAN
replication, two-node strong clusters, direct brick mounts, NFS/SMB serving,
multi-region operation, and writable shared `mmap`.

The exploratory documents in [`arch/`](arch/) and
[`docs/directory-daemon.md`](docs/directory-daemon.md) are retained for
history. They describe a watcher-based replication approach that is superseded
by this foundation and must not be used as an implementation contract.
