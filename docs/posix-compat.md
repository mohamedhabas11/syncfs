# POSIX Compatibility Matrix

Per-operation status (strict profile, technical spec section 3 criteria).
Evidence: unit tests in `internal/fusefs/posix_test.go` + `m3_01_test.go`,
live stat-root mount in the container fleet (`TestFUSEmountSmoke`).
The Handler surface is a flat root-children tree; the full go-fuse child
tree (`NewPersistentInode`, `Readdir`, `Forget`) is pending, so kernel
use is stat-root only (see `docs/known-limitations.md` #15).

| Operation | Status | Test | Notes |
| --- | --- | --- | --- |
| create (`O_CREAT`) | DONE (in-memory surface) | `TestCompatProfileSurface` | `CreateFile` dup -> EEXIST; FUSE `Open` does not interpret `O_CREAT`/`O_EXCL` flags (no flag-gated FUSE create; `O_EXCL` via the Handler method only) |
| open | DONE | `TestCallerCredentialsEnforcedInLookup`, `TestOpenReleaseFsyncLifecycle` | direct-I/O policy via adapter; credential check |
| Lookup + caller creds | DONE | `TestCallerCredentialsEnforcedInLookup` | `fuse.FromContext` uid/gid enforced (EACCES); Lookup returns nil inode with entry attrs (kernel traversal pending, #15) |
| direct-I/O handles | DONE | `TestOpenReleaseFsyncLifecycle` | `FOPEN_DIRECT_IO`; mutable handles bypass page cache |
| read/write | DONE (in-memory surface) | `TestCompatProfileSurface`, `TestWriteFileFailsClosedOnSyncerError` | write at offset; append at EOF; `MaxFileBytes` (16 MiB Handler) caps growth with EFBIG; syncer error rolls back + EIO |
| truncate | DONE (in-memory surface) | `TestCompatProfileSurface` | zero-extend; caps: 16 MiB (Handler, `internal/fusefs/posix.go:154`) / 32 MiB (state machine, `internal/metadata/metadata.go:169`) -> EFBIG |
| directories (mkdir/rmdir/readdir) | DONE (in-memory surface) | `TestCompatProfileSurface` | flat root children only; Rmdir non-empty returns ENOTEMPTY |
| hard links | DONE | `TestHardLinkSymlinkFsyncDir` | dirs refused EPERM; dup EEXIST |
| symlinks | DONE | `TestHardLinkSymlinkFsyncDir` | target stored; resolved via Readlink; not yet traversed by FUSE |
| rename (atomic, one command) | PARTIAL (two surfaces) | `TestCompatProfileSurface`, `TestHardLinkSymlinkFsyncDir` | Handler: flat-root moves only (overwrite allowed with EISDIR/ENOTDIR/ENOTEMPTY checks); state machine: two-parent moves in one command but existing-destination rename refused (`ErrAlreadyExists`, no-overwrite). Slash names EINVAL (Handler). |
| fsync / flush / release lifecycle | DONE (handle layer) | `TestOpenReleaseFsyncLifecycle` | Release idempotent; released Fsync/Flush EBADF |
| fsyncdir | DONE (handle layer; coordinator seam pending) | `TestHardLinkSymlinkFsyncDir` | files ENOTDIR; durability still via the M5 coordinator seam |
| uid/gid/mode | DONE | `TestCallerCredentialsEnforcedInLookup` | enforced in Lookup/Open |
| strict TTLs | DONE | `TestStrictTimeoutsZero` | zero entry/attr/negative timeouts |
| errno table | DONE | `TestErrnoTable` | exact Linux errno map; unknown -> EIO |
| flock / POSIX range locks | DONE (manager) | `locks` tests | manager GREEN (family queues, `BlockingGrantCtx`, `ErrDeadlock`); `BindProto` decodes inode+range but state-machine `LOCK_GRANT` is a no-op — Raft-ordered E2E wiring pending |
| xattrs / ACLs | PARTIAL | `TestXattrUserNamespaceOnly` | user.* with non-empty suffix live (Handler EINVAL for bare `user.`/NUL, EPERM for `security.selinux`, EOPNOTSUPP otherwise; state machine rejects non-`user.*` as `ErrCommandFailed`); ACL policy enforcement pending |
| mmap writable | EXCLUDED (pending handler) | — | no FUSE `Mmap` handler today, so the kernel gets the default (ENOSYS), not a guaranteed `EOPNOTSUPP`; excluded by design, explicit handler pending (spec §2). |
| O_DIRECT | EXCLUDED | `TestOpenRejectsODIRECT` | `EINVAL` in strict mode on Linux via build-tagged `oDirectFlag` (`internal/fusefs/odirect_linux.go`); `oDirectFlag` is 0 off-Linux so the check is Linux-only |
| NFS/SMB | EXCLUDED | — | architecture boundary |
