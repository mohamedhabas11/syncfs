# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- M3-01 completion: credential extraction, direct-I/O handles, errno table, lifecycle smoke.
- M3-02: POSIX compatibility surface (create/read/write/rename/fsync directories).
- M3-03: client operation journal + fail-closed session semantics.
- M4-01: epoch-based chunk GC. M4-02: membership replacement. M4-03: CLI + systemd. M4-04: fault harness.
- Docs site scaffolding (MkDocs/Material, mike), status.md, ADRs, release prep.

### Known limitations
- Pre-1.0; not production ready.
- xattr/ACL namespaces fail closed (DEFERRED, never silent).
- Coordinator-backed FUSE/persistence wiring (M5) is next.
