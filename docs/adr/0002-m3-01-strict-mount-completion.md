# ADR 0002 — M3-01 strict mount completion (caller creds, direct-I/O, lifecycle)

**Status:** Accepted (2026-11-08)

## Context

The FUSE mount skeleton (eba1e4e) mounted on Linux but discarded caller
credentials (`_ = caller` in Lookup/Open), returned a bare `nil` handle,
used mount defaults that allow kernel metadata caching, and discarded
coordinator errors on writes. Its Getattr had the wrong `*Attr` signature
so the go-fuse bridge fell back to defaults.

## Decision

- Extract Linux caller credentials via `fuse.FromContext` in Lookup/Open/
  Getattr and enforce uid/gid/mode (EACCES; root bypass).
- Gate mutable handles to direct I/O (`FOPEN_DIRECT_IO`) and hand back a
  real `fileHandle` implementing Release/Flush/Fsync. Released-handle
  Fsync/Flush fail closed with EBADF; Release is idempotent.
- Enforce strict TTLs: zero entry/attr/negative timeouts, no AllowOther
  (`FSOptions`/`StrictTimeouts`).
- Expand the exact Linux errno table (`ENODATA`, `ELOOP`, `ENAMETOOLONG`,
  `EFBIG`, `ESPIPE`); unknown symbols map to EIO.
- Fail closed on writes: syncer errors roll back and return EIO.

## Consequences

- `TestFUSEmountSmoke` now exercises the real Lookup/Getattr path (nil-handler
  panic fixed by requiring a real Handler).
- M3-02 gains Link/Readlink/FsyncDir and slash-name validation (EINVAL).
- Coordinator persistence wiring stays M5 (syncer seam only); durability
  here is handle + error discipline, not pack persistence.
