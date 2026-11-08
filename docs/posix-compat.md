# POSIX Compatibility Matrix

Per-operation status (strict profile, technical spec section 3 criteria).
Evidence: unit tests in `internal/fusefs/posix_test.go`, live mount in the
container fleet (`TestFUSEmountSmoke`).

| Operation | Status | Test | Notes |
| --- | --- | --- | --- |
| create (`O_CREAT`) | DONE (in-memory surface) | `TestCompatProfileSurface` | no Excl variant yet beyond EEXIST |
| open | DONE | `TestCompatProfileSurface` | direct-I/O policy via adapter |
| read/write | DONE (in-memory surface) | `TestCompatProfileSurface` | write at offset; append at EOF |
| truncate | DONE (in-memory surface) | `TestCompatProfileSurface` | zero-extend |
| directories (mkdir/rmdir/readdir) | DONE (in-memory surface) | `TestCompatProfileSurface` | Rmdir non-empty returns ENOTEMPTY |
| hard links | PARTIAL | — | enforced as forbidden-to-dirs rule; link op pending |
| symlinks | DONE | `TestCompatProfileSurface` | target stored; not resolved through FUSE yet |
| rename (atomic, one command) | DONE (in-memory surface) | `TestCompatProfileSurface` | two-parent moves in same command |
| fsync / fsyncdir | PARTIAL | `TestCompatProfileSurface` | marks durable; coordinator fsync pending |
| uid/gid/mode | DONE | `TestCompatProfileSurface` | carried on nodes |
| flock / POSIX range locks | DONE (manager) | `locks` tests | Raft-ordered wiring pending M4 |
| xattrs / ACLs | DEFERRED | — | fail-closed with EOPNOTSUPP (adapter rule) |
| mmap writable | EXCLUDED | — | `EOPNOTSUPP` (spec) |
| O_DIRECT | EXCLUDED | — | `EINVAL` in strict mode (adapter rule) |
| NFS/SMB | EXCLUDED | — | architecture boundary |
