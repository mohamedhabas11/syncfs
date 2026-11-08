# POSIX Compatibility Matrix

Per-operation status (strict profile, technical spec section 3 criteria).
Evidence: unit tests in `internal/fusefs/posix_test.go` + `m3_01_test.go`,
live mount in the container fleet (`TestFUSEmountSmoke`).

| Operation | Status | Test | Notes |
| --- | --- | --- | --- |
| create (`O_CREAT`) | DONE (in-memory surface) | `TestCompatProfileSurface` | no Excl variant yet beyond EEXIST |
| open | DONE | `TestCallerCredentialsEnforcedInLookup`, `TestOpenReleaseFsyncLifecycle` | direct-I/O policy via adapter; credential check |
| Lookup + caller creds | DONE | `TestCallerCredentialsEnforcedInLookup` | `fuse.FromContext` uid/gid enforced (EACCES) |
| direct-I/O handles | DONE | `TestOpenReleaseFsyncLifecycle` | `FOPEN_DIRECT_IO`; mutable handles bypass page cache |
| read/write | DONE (in-memory surface) | `TestCompatProfileSurface`, `TestWriteFileFailsClosedOnSyncerError` | write at offset; append at EOF; syncer error rolls back + EIO |
| truncate | DONE (in-memory surface) | `TestCompatProfileSurface` | zero-extend |
| directories (mkdir/rmdir/readdir) | DONE (in-memory surface) | `TestCompatProfileSurface` | Rmdir non-empty returns ENOTEMPTY |
| hard links | DONE | `TestHardLinkSymlinkFsyncDir` | dirs refused EPERM; dup EEXIST |
| symlinks | DONE | `TestHardLinkSymlinkFsyncDir` | target stored; resolved via Readlink; not yet traversed by FUSE |
| rename (atomic, one command) | DONE (in-memory surface) | `TestCompatProfileSurface`, `TestHardLinkSymlinkFsyncDir` | two-parent moves in same command; slash names EINVAL |
| fsync / flush / release lifecycle | DONE (handle layer) | `TestOpenReleaseFsyncLifecycle` | Release idempotent; released Fsync EBADF |
| fsyncdir | DONE (handle layer; coordinator seam pending) | `TestHardLinkSymlinkFsyncDir` | files ENOTDIR |
| uid/gid/mode | DONE | `TestCallerCredentialsEnforcedInLookup` | enforced in Lookup/Open |
| strict TTLs | DONE | `TestStrictTimeoutsZero` | zero entry/attr/negative timeouts |
| errno table | DONE | `TestErrnoTable` | exact Linux errno map; unknown -> EIO |
| flock / POSIX range locks | DONE (manager) | `locks` tests | Raft-ordered wiring via BindProto (family, range, release) |
| xattrs / ACLs | PARTIAL | `TestXattrUserNamespaceOnly` | user.* live; ACL policy enforcement pending; others EOPNOTSUPP/EPERM |
| mmap writable | EXCLUDED | — | `EOPNOTSUPP` (spec) |
| O_DIRECT | EXCLUDED | — | `EINVAL` in strict mode (adapter rule) |
| NFS/SMB | EXCLUDED | — | architecture boundary |
