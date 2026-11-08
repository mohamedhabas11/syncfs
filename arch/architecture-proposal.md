# Architecture Proposal — Directory Replication Daemon

> **Superseded exploratory draft.** This document describes a watcher-based
> native-directory replication daemon. That model cannot provide the shared
> POSIX consistency, cache coherency, or quorum durability now required by this
> project. It is retained for history only; implement against
> [`.spec/technical_spec.md`](../.spec/technical_spec.md) and
> [`docs/architecture.md`](../docs/architecture.md).

**Status:** Draft (architecture phase)
**Companion to:** `.spec/directory-replication-daemon.md`

Derived from the design spec now that the spec phase is considered laid out. This is deliverable #1 of the pre-code checklist.

## 1. Context

- Single Go daemon, single binary, config-driven, Linux only, no Kubernetes.
- Replicates declared local directories across 2–3+ nodes on ordinary mounted filesystems (ext4/XFS).
- Native filesystem only; no custom FS, block layer, or object storage.
- Local state (change journal, peer state) in an embedded store, not an external database.
- Local file operations never block on remote nodes unless the directory's policy requires it.

## 2. Component overview

| Component | Responsibility |
|---|---|
| Config | Loads and validates YAML directory declarations. |
| Watcher | Change detection (inotify/fanotify + periodic reconcile scan). |
| Ingest | Local move/copy into pool + journaling. |
| Journal | Embedded local store: change records and committed position. |
| Transport | Async replication of committed changes to peers, policy-driven. |
| Lease manager | Quorum lease grant/renew/revoke with fencing tokens. |
| Election | Leader election among healthy candidates. |
| Conflict resolver | Reconcile on rejoin (bidirectional mode). |
| Safety | HALTED detection, quarantine handling, mass-delete guard. |

## 3. Write path

```
app write → local native FS → watcher → ingest → journal commit (advances committed position) → transport → peer
```

- Single-writer / lease modes restrict who may write before the path is entered.
- Journal commit defines the committed position; transport ships committed changes asynchronously.
- Local operations never block on remote nodes unless the directory's mode/policy requires it.

## 4. Read path

- Local native filesystem is the storage medium for reads.
- Leader authoritative; followers serve reads from their replica.
- During CATCHING_UP, reads are served from stale state (marked stale) until replay completes.

## 5. Failure handling

- Node states (LEADER / FOLLOWER / FENCED / CANDIDATE / CATCHING_UP / QUARANTINED / HALTED) map directly onto component behaviour (lease expiry, election, quarantine, safety checks).
- Failure scenarios (leader crash, partitions, watcher loss, daemon hang) are handled via the mechanisms in the spec: lease TTL + self-fencing, ExecStopPost remount, watchdog, quarantine.

## 6. Mode wiring

- **One-way async:** primary designation + replica change stream; manual promotion bumps epoch; divergent returning primary → quarantine.
- **Bidirectional async:** all nodes accept writes; rejoin uses per-file version vectors; conflicts become policy-named conflict copies; delete-vs-edit resolves to keeping the edit; mass-delete breaker active.
- **Single-writer lease:** quorum-granted lease with fencing; only lease holder writes; others read-only.

## 7. Constraints conformance

- Single Go binary ✅
- No Kubernetes dependency ✅
- Native filesystem, no custom FS / block / object storage ✅
- Embedded store, not external DB ✅
- Local ops don't block unless policy requires ✅

## 8. Open architecture questions

1. Change detection: fanotify vs inotify + periodic reconcile scan (open spec Q1).
2. Journal format and embedded store backend.
3. Transport mechanism between nodes (local IPC? TCP?).
4. Batching / backpressure (open spec Q2).
5. Quarantine layout: `.dirsync/quarantine/epoch-N/`.
6. Checksum / integrity verification (open spec Q8).

## 9. Next steps

- Mode comparison spec sheet.
- Failure-state model spec sheet.
- Go package layout sketch.
- Replication protocol sketch (change journal, committed position, epoch checks).
