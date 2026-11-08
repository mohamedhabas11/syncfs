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
| Durability | Library path: chunks are quorum-durable before the metadata commit (`Coordinator.WriteChunks` + Raft, Inv.3). FUSE `Fsync`/`FsyncDir` are seam-level no-ops today; E2E coordinator wiring is M5 work (see `docs/known-limitations.md` #6/#17). |
| Atomic creation | `mkdir` and `rename` are serialized by the volume metadata state machine (rename = one command). `O_CREAT|O_EXCL` is only the Handler `CreateFile` dup -> EEXIST; FUSE `Open` does not yet interpret `O_CREAT`/`O_EXCL` flags. |
| Locks | Manager-level cluster-coherent `flock` and POSIX byte-range locks (family queues, cancellable wait, `EDEADLK`); lock ownership is tied to the client session and released after session expiry. Raft-ordered E2E wiring is pending. |
| Metadata | uid/gid, mode bits, sticky/setgid directory behavior, timestamps, `stat`, `statx`, and `fsyncdir` (handle-layer surface; kernel traversal pending (#15)). |
| Links | Symlinks and hard links within a volume; unlink of an open file retains the inode until the last handle closes. |
| Extended attributes | `user.*` with non-empty suffix is live; POSIX ACL xattrs are planned (enforcement pending); unsupported namespaces fail explicitly (EOPNOTSUPP/EPERM at the Handler, `ErrCommandFailed` in the state machine). |
| Cache behavior | Coherent cache leases with explicit FUSE invalidation. A disconnected strict mount never knowingly serves stale cache data. |
| Quorum loss | New uncached reads and writes fail instead of silently becoming eventually consistent. |

## Intentional MVP exclusions

| Feature | MVP behavior | Why |
| --- | --- | --- |
| NFS/SMB serving | Not provided | A protocol gateway would add another cache/lock consistency contract before the core is proven. |
| Direct local-brick access | Forbidden | Bypasses fencing, locks, metadata ordering, and cache invalidation. |
| Shared writable `mmap` | excluded (pending explicit handler; today the kernel gets the default ENOSYS, not a guaranteed `EOPNOTSUPP`) | Correct distributed page-cache coherence needs a separate design. The enforcement point *should be* a `mmap` handler returning `EOPNOTSUPP`; that handler is not yet implemented. |
| Device nodes, sockets, FIFOs | `EOPNOTSUPP` (policy) | They are not appropriate for a replicated shared-home namespace. |
| `O_DIRECT` | `EINVAL` on Linux in strict mode (per man 2 open: unsupported flags map to `EINVAL`, not `EOPNOTSUPP`); build-tagged, no check off-Linux | The cache and durability contract is implemented by SyncFS, not bypassed. |
| Cross-volume rename/link | `EXDEV` | POSIX-compatible and avoids distributed two-volume transactions. |
| Wide-area / multi-region cluster | Unsupported | The latency and partition model conflicts with low-latency quorum operations. |
| Two storage voters | Unsupported for strict writes | A two-node system cannot distinguish a failed peer from a partition without a third voting failure domain. |
| Offline writes | Unsupported | SyncFS is a shared directory, not a conflict-resolving sync tool. |

## Error behavior

| Condition | Error exposed to the application |
| --- | --- |
| No leader or no quorum | `EIO` after the configured request deadline (`EHOSTDOWN` is a network-layer errno with no `open(2)`/FUSE precedent — do not surface it; use `EIO` uniformly. If a future revision wants `EHOSTDOWN`, it must cite a man-2 precedent). |
| Known stale file handle | `ESTALE` (FUSE `fh`/generation trigger MUST be defined in M3-01 — an `fh` maps to inode ID + generation; `ESTALE` means generation mismatch, not NFS handle semantics). |
| Unsupported filesystem operation | `EOPNOTSUPP` only where the `ENOSYS`-vs-`EOPNOTSUPP` override table (M3-01) names it; `O_DIRECT` → `EINVAL` (see exclusions). |
| Lock conflict | Block for blocking lock calls; otherwise `EAGAIN`. Deadlock: `EDEADLK` (V1 leader detection, M2-06). |
| Permission rejection | `EACCES` or `EPERM`. |
| Storage capacity unavailable on a write quorum | Coordinator refuses with `ErrNoQuorum` before commit; the FUSE surface maps unmet-quorum writes to `EIO` (fail closed) with `ENOSPC` used where a capacity code is named. |
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
