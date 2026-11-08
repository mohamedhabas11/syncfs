# SyncFS Compatibility Profile

SyncFS aims at the POSIX operations that HA service shared homes actually use.
It does not claim full transparent compatibility with every Linux filesystem or
every NFS/SMB edge case. A workload is supported only when it stays within this
profile and passes its integration suite against a three-node fault-injection
cluster.

## MVP-supported behavior

| Feature | Contract |
| --- | --- |
| Paths and directories | Hierarchical UTF-8 byte names except `/` and NUL; atomic `mkdir`, `rmdir`, `unlink`, and same-volume `rename`. |
| Regular files | Create, open, read, write, truncate, append, seek, sparse holes, and atomic replacement. |
| Visibility | A successful mutating FUSE call is globally visible in one Raft order. Local dirty data is not acknowledged as globally committed. |
| Durability | `write`, `fsync`, `fdatasync`, and directory-changing operations complete only after their required data and metadata are quorum durable. |
| Atomic creation | `O_CREAT|O_EXCL`, `mkdir`, and `rename` are serialized by the volume metadata state machine. |
| Locks | Cluster-coherent `flock` and POSIX byte-range locks; lock ownership is tied to the client session and released after session expiry. |
| Metadata | uid/gid, mode bits, sticky/setgid directory behavior, timestamps, `stat`, `statx`, and `fsyncdir`. |
| Links | Symlinks and hard links within a volume; unlink of an open file retains the inode until the last handle closes. |
| Extended attributes | `user.*` and POSIX ACL xattrs are planned in the first compatibility milestone; unsupported namespaces fail explicitly. |
| Cache behavior | Coherent cache leases with explicit FUSE invalidation. A disconnected strict mount never knowingly serves stale cache data. |
| Quorum loss | New uncached reads and writes fail instead of silently becoming eventually consistent. |

## Intentional MVP exclusions

| Feature | MVP behavior | Why |
| --- | --- | --- |
| NFS/SMB serving | Not provided | A protocol gateway would add another cache/lock consistency contract before the core is proven. |
| Direct local-brick access | Forbidden | Bypasses fencing, locks, metadata ordering, and cache invalidation. |
| Shared writable `mmap` | `EOPNOTSUPP` | Correct distributed page-cache coherence needs a separate design. |
| Device nodes, sockets, FIFOs | `EOPNOTSUPP` | They are not appropriate for a replicated shared-home namespace. |
| `O_DIRECT` | `EOPNOTSUPP` in strict mode | The cache and durability contract is implemented by SyncFS, not bypassed. |
| Cross-volume rename/link | `EXDEV` | POSIX-compatible and avoids distributed two-volume transactions. |
| Wide-area / multi-region cluster | Unsupported | The latency and partition model conflicts with low-latency quorum operations. |
| Two storage voters | Unsupported for strict writes | A two-node system cannot distinguish a failed peer from a partition without a third voting failure domain. |
| Offline writes | Unsupported | SyncFS is a shared directory, not a conflict-resolving sync tool. |

## Error behavior

| Condition | Error exposed to the application |
| --- | --- |
| No leader or no quorum | `EHOSTDOWN` after the configured request deadline. |
| Known stale file handle | `ESTALE`. |
| Unsupported filesystem operation | `EOPNOTSUPP`. |
| Lock conflict | Block for blocking lock calls; otherwise `EAGAIN`. |
| Permission rejection | `EACCES` or `EPERM`. |
| Storage capacity unavailable on a write quorum | `ENOSPC`. |
| Lost FUSE transport / unresolvable ambiguous mutation | `EIO` or `ENOTCONN`; the client exposes the operation ID for operator diagnosis. |

## Application qualification

Before declaring an application supported, its deployment must pass:

1. its own HA/shared-home test suite on at least two application hosts;
2. concurrent metadata, lock, rename, and restart tests against the FUSE mount;
3. leader loss, follower loss, partition-minority, client reconnect, and node
   replacement fault cases; and
4. a workload benchmark that records tail latency at the intended durability
   mode and hardware profile.

Jira, Confluence, repository managers, and artifact services are target
workload classes, not compatibility certifications until this gate passes.
