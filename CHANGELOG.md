# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-08

Pre-1.0, not production ready. Foundation (M1–M4) with M5 qualification
deferred (ADR-0001). See `docs/releases/v0.1.0.md` and
`docs/spec/status.md`.

### Added

- M3-01 completion: caller-credential extraction in Lookup/Open/Getattr,
  direct-I/O handle policy (`FOPEN_DIRECT_IO`), exact Linux errno table,
  Release/Fsync/Flush lifecycle, strict zero metadata TTLs, fail-closed
  writes (ADR-0002).
- M3-02: POSIX surface additions (hard Links, Readlink, FsyncDir,
  slash-name validation) and honest matrix in `docs/posix-compat.md`.
- M3-03: durable client operation journal (atomic `journal.json`) and
  reconnect status folding; invalidate-all + FUSE abort on drop.
- M4-01: epoch-based chunk GC. M4-02: joint-consensus promotion +
  draining. M4-03: admin CLI validation + systemd units. M4-04: fault
  harness kinds (kill, pause, partition, delay, duplication, disk-full,
  corrupted pack, client restart).
- M2-06 locks fix: family separation, same-owner merge/convert,
  BlockingGrant wake, BindProto range/inode/release handling.
- Benchmark harness (`test/benchmark/workload_test.go`) with real in-process
  numbers in `docs/benchmarks/results.json` (NOT §11 qualification).
- Docs site (MkDocs/Material, mike) + `make docs-gen` note + status.md.

### Changed

- CLI reference now matches the real validation behavior (exit 2 usage,
  exit 1 missing file/dir) and admin subcommands.
- Known limitations expanded with adversarial-audit caveats (#7–#13).

### Known limitations

- Pre-1.0; not production ready.
- xattr/ACL namespaces fail closed (never silent).
- Coordinator-backed persistence wiring is M5; WAL/chunk/metadata caveats
  listed in `docs/known-limitations.md`.
- Benchmark numbers are in-process only (no QUIC/FUSE/10 GbE).

## [Unreleased]

### Added

- (empty — cut for v0.1.0; see [0.1.0] above)

### Known limitations

- See [0.1.0] and `docs/known-limitations.md`.
