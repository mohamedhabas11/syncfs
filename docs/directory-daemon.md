# syncfs — Directory Management Daemon

> **Superseded exploratory draft.** This document describes a local ingest
> daemon with no multi-node contract. It is retained for history only. The
> current project is a FUSE-mediated, quorum-replicated shared-directory service;
> use `.spec/technical_spec.md` and
> [`docs/architecture/`](architecture/index.md) as the implementation contract.

> Do not treat as current spec. **Superseded (2026-11-08, D-001).** Status: **Draft v0.1.0** · Updated: 2026-11-08

## 1. Summary

`syncfs` is a single-node daemon that manages directories declared in its YAML configuration file. It watches those directories and ingests new content into a local storage pool on the same host.

It implements a **storage ring**: there is no client/server model. Each node brings and manages its own storage mount into the pool, and the daemon operates entirely on that node's local filesystem.

Hard constraints (non-negotiable):

1. No metadata database — all state is derived from the filesystem itself.
2. No S3 or any remote live backend.
3. Must operate on top of regular local filesystems (ext4, XFS, …) including LVM logical volumes that support live expansion.
4. Must respect POSIX rules and the POSIX ownership model on Linux.

## 2. Goals / Non-Goals

### Goals

- YAML-driven managed directories: an operator declares sources, the daemon manages them.
- Ingest new and changed content from sources into the pool.
- Crash-safe, idempotent operation without a metadata database.
- POSIX-compliant handling: mode bits (incl. setuid/setgid/sticky), uid/gid ownership, POSIX ACLs, symlinks.

### Non-Goals (decided)

- **No metadata database.** State lives in the filesystem.
- **No S3 / remote backend.** Local filesystem pool only.
- **No client/server model.** Local storage ring per node.
- Multi-node federation is out of scope for the initial version (each node is its own ring member).

## 3. Terminology

| Term | Meaning |
| --- | --- |
| **Source** | Directory declared in the YAML config (`source:`). Normally a mount point. |
| **Pool** | Logical storage area named in the config (`pool:`). Maps to a directory under `pool.root`. |
| **Managed content** | Files that have been ingested into a pool. |

## 4. Requirements

### 4.1 Functional requirements

- **FR-1 — Config-driven.** Managed directories come from the YAML config file.
- **FR-2 — Watch & ingest.** The daemon detects new/changed content in managed sources and ingests it into the pool.
- **FR-3 — Crash-safe & idempotent.** Re-running must be safe; no duplicate or lost content.
- **FR-4 — POSIX compliance.** Ownership, mode, ACL, symlink handling must follow POSIX semantics.
- **FR-5 — Operations.** Structured logging, graceful shutdown, CLI for manual reconcile.

### 4.2 Constraints (non-negotiables)

- No metadata DB.
- No remote backend (S3 etc.).
- Plain local FS (ext4/XFS) + LVM live expansion.
- POSIX Linux only.
- No client/server architecture.

## 5. State Model — No Metadata Database

Because a metadata DB is forbidden, all state must be derivable from the filesystem.

Design decision:

- Each source maps to one pool directory.
- Managed content is keyed either by:
  - **mirror** — source-relative path (`<pool>/<source-relative-path>`), or
  - **flat** — content address (`<pool>/<sha256(content)>`, enabling dedupe).
- Before ingesting a candidate, the daemon compares content:
  - identical content already present → **skip** (idempotent, dedupe),
  - different content → **re-ingest** (e.g. file changed since last run).

Consequences:

- No DB required; state is the pool directory itself.
- Crash safety comes from atomic writes (temp file + fsync + rename), so restarts converge to the same state.
- The daemon must not rely on filesystem-specific features beyond basic POSIX semantics, per the constraint in §4.2.

## 6. Configuration

Config file: `/etc/syncfs/config.yaml` (override with `--config`).

```yaml
version: 1

daemon:
  id: homelab-01
  log_level: info
  poll_interval_seconds: 30

pool:
  root: /mnt/pool

sources:
  - source: /mnt/usb-data
    pool: usb-data
    action: move          # move | copy
    naming: mirror        # mirror | flat
    mode: "0755"          # pool-side mode; default: preserve source
    owner: "1000:1000"   # pool-side owner uid:gid; default: preserve source
    recursive: true
    include:
      - "**/*"
    exclude:
      - "**/*.tmp"
```

### Config fields

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `version` | int | `1` | Config schema version. |
| `daemon.id` | string | – | Node identifier used in logs/events. |
| `daemon.log_level` | string | `info` | Log level. |
| `daemon.poll_interval_seconds` | int | `30` | Fallback full-scan interval; also reconcile cadence. |
| `pool.root` | string | – | Root of the storage pool on local FS. |
| `sources[]` | list | – | Managed sources (at least one required). |
| `sources[].source` | string | – | Absolute path of source dir (mount point preferred). |
| `sources[].pool` | string | – | Logical pool name; destination lives at `pool.root/<pool>`. |
| `sources[].action` | enum | `move` | `move` (ingest then remove from source) or `copy` (keep source). |
| `sources[].naming` | enum | `mirror` | `mirror` preserves source-relative layout; `flat` uses content-hash names. |
| `sources[].mode` | string | preserve | Octal mode applied to pool copies (e.g. `"0755"`). |
| `sources[].owner` | string | preserve | Owner as `uid:gid` applied to pool copies. |
| `sources[].recursive` | bool | `true` | Recurse into subdirectories. |
| `sources[].include` / `sources[].exclude` | list | all / none | Glob filters (e.g. `**/*.tmp`). |

Destination layout:

- `naming: mirror` → `<pool.root>/<pool>/<source-relative-path>`
- `naming: flat` → `<pool.root>/<pool>/<sha256(content)>`

## 7. Ingest Pipeline

For each source (per reconcile cycle / event):

1. **Resolve source.** If missing or not a mount point → log warning, retry later. No crash.
2. **Enumerate** managed entries per include/exclude + `recursive`.
3. **Decide** whether candidate is already ingested (content comparison; §5).
4. **If not ingested**, ingest:
   - Copy to a temp file inside the destination directory.
   - `fsync` file → apply `mode`/`owner` (+ACL/xattr preservation) → `fsync` dir.
   - Atomic `rename` into final destination name.
   - `fsync` destination directory.
5. **Move vs copy:** same `st_dev` → atomic `rename`; different device → temp copy + unlink source. (If `action: copy`, source is never removed.)
6. **Errors:** log and leave the candidate for the next cycle; convergence is guaranteed by idempotency (§5).

## 8. Watching

- **Primary:** inotify (`inotify_add_watch`) on source mount points for `IN_CREATE` / `IN_MOVED_TO` / `IN_CLOSE_WRITE`, with debounced ingest triggers.
- **Fallback / authority:** periodic full scan every `daemon.poll_interval_seconds` (default 30s), which also serves as the reconcile pass.

Because the periodic scan is authoritative, watching is an optimization, not the source of truth.

## 9. LVM / Live Storage Expansion

- Sources are expected to be mount points; the daemon verifies source paths against the live mount table (e.g. `/proc/mounts`) on each cycle.
- **Live LVM expansion** (online LV grow) keeps the same mount alive → daemon behavior unchanged.
- **LV unmount/remount or LV replacement** → source path now points at a new inode; the daemon must re-resolve the source and re-establish its watch. The per-cycle mount re-validation handles this.

The daemon never assumes a static device → path mapping; it re-resolves from the mount table.

## 10. POSIX Compliance

- Apply **chown before chmod** (chown may clear setuid/setgid bits); set full mode including `setuid` (04000), `setgid` (02000), sticky (01000).
- Preserve or override via config: `owner` (`uid:gid`), `mode` (octal).
- ACLs: use `acl(5)` interfaces (`getfacl`/`setfacl`) when available; preserve by default, allow config override.
- Symlinks: recreate as symlinks with the same target; never follow.
- xattrs: preserve `user.*` attributes when possible; do not attempt `security.*` / `trusted.*` (kernel-restricted).

## 11. Operations

- systemd unit sketch:

```ini
[Unit]
Description=syncfs directory management daemon
After=local-fs.target

[Service]
Type=simple
ExecStart=/usr/bin/syncfs daemon --config /etc/syncfs/config.yaml
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

- CLI surface (draft): `syncfs daemon`, `syncfs reconcile`, `syncfs status`.

## 12. Example

1. Operator creates pool root: `mkdir -p /mnt/pool`.
2. Operator adds mount to `/etc/fstab`, mounts at `/mnt/usb-data`.
3. Operator writes `/etc/syncfs/config.yaml` declaring `/mnt/usb-data` → pool `usb-data`.
4. `syncfs daemon` starts, resolves source, ingests new files into `/mnt/pool/usb-data/...`.
5. Restart / `syncfs reconcile` → already-ingested content is detected and skipped.

## 13. Open Questions / Decisions Log

- Default `action` is `move`; alternative `copy` available.
- Default `naming` is `mirror`; alternative `flat` (content-addressed) available.
- ACL preservation default: enabled.
- Poll interval default: 30s (tunable).

## 14. Out of Scope

- Multi-node / multi-host pooling.
- Remote or S3 backends.
- Metadata database of any kind.
- Client/server protocol.
