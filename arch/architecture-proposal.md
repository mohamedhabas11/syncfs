> **ARCHIVED (2026-11-08) — superseded as MVP architecture by decision D-001 (research/07-design-fork-reconciliation.md §3.5).** The watcher + pull-based data plane below is superseded. MVP architecture is the FUSE-mounted strict volume (.spec/technical_spec.md §§5, 8). Retained as reference for post-MVP tiers only. Do not implement MVP scope from this file.

# Architecture Proposal — Directory Replication Daemon

**Status:** Draft (architecture phase; updated with consensus decision)
**Companion to:** `.spec/directory-replication-daemon.md`

## 1. Context

- Single Go daemon, single binary, config-driven, Linux only, no Kubernetes.
- Replicates declared local directories across 2–3+ nodes on ordinary mounted filesystems (ext4/XFS).
- Native filesystem only; no custom FS / block / object storage; no external DB; embedded private state allowed (Raft log + stable store); state dir outside managed dirs.
- Same binary everywhere, no dedicated server role, pull-based data plane; leader elected.

## 2. Component overview

| Component | Responsibility |
|---|---|
| Config | Loads and validates YAML directory declarations. |
| Watcher | Change detection (inotify/fanotify + periodic reconcile scan). |
| Ingest | Local move/copy into pool + journaling. |
| Raft group | Consensus: `hashicorp/raft`; single group, cluster-wide; per-directory leases as state-machine data. |
| Transport | Pull-based: followers pull committed changes from leader's journal. |
| Lease manager | Quorum lease grant/renew/revoke; fencing token = per-directory log index of lease-grant entry; self-fencing via leader lease timeout → leadership channel → fence. |
| Election | Leader election (via Raft); operator override possible. |
| Conflict resolver | Reconcile on rejoin (bidirectional mode). |
| Safety | HALTED detection, quarantine handling, mass-delete guard; app-level read-only fence (EBUSY fallback); ExecStopPost best-effort. |

## 3. Write path

```
app write → local native FS → watcher → ingest → Raft (log + commit; per-directory lease entries) → transport (pull) → peer
```

- Single-writer / lease modes restrict who may write before the path is entered.
- Raft commit defines the committed position; transport ships committed changes asynchronously (pull-based).
- Local operations never block on remote nodes unless the directory's mode/policy requires it.

## 4. Read path

- Local native filesystem is the storage medium for reads.
- Leader authoritative; followers serve reads from their replica.
- During CATCHING_UP, reads are served from stale state (marked stale) until replay completes.

## 5. Failure handling

- Node states (LEADER / FOLLOWER / FENCED / CANDIDATE / CATCHING_UP / QUARANTINED / HALTED) map onto component behaviour (lease expiry, election, quarantine, safety checks).
- Self-fencing: leader lease timeout → leadership channel → fence (RO, stop writes). `ExecStopPost` best-effort; `remount,ro` EBUSY caveat; crash-vs-hang distinction; fast restart within lease TTL resumes via journal replay; fencing required only when the node can't prove a valid lease before the safety margin expires.

## 6. Consensus layer (decided: HashiCorp Raft)

- Adopt `hashicorp/raft`.
- Single Raft group, cluster-wide; per-directory leases as state-machine data.

**Mapping:**

- Voters / witness / learners:
  - Witness: voter whose log holds only small metadata; file data never goes through Raft.
  - Remote/geographic nodes: join as non-voters (`AddNonvoter`) and follow asynchronously.
- Self-fencing: leader lease timeout → step down if quorum unreachable → notify via leadership channel → trigger fence (RO, stop writes).
- Fencing token: per-directory log index of lease-grant entry = epoch; Raft term is cluster-wide; per-directory token is right granularity; replicas reject older.

**Library provides:** leader election, WAL, log replication, leader lease timeout, leadership-channel notification.

**Library does not provide:** app-level read-only fence (EBUSY fallback); catch-up/quarantine/conflicts/POSIX apply; operator recovery (manual).

## 7. Mode wiring

- **One-way async:** primary designation + replica change stream; manual promotion bumps epoch; divergent returning primary → quarantine.
- **Bidirectional async:** all nodes accept writes; rejoin uses per-file version vectors; conflicts become policy-named conflict copies; delete-vs-edit keeps the edit; mass-delete breaker active.
- **Single-writer lease:** quorum-granted lease with fencing; only lease holder writes; others read-only.

## 8. Constraints conformance

- Single Go binary ✅
- No Kubernetes dependency ✅
- Native filesystem, no custom FS / block / object storage ✅
- No external DB; embedded Raft log + stable store in private state dir ✅
- Local ops don't block unless policy requires ✅
- Same binary everywhere, no dedicated server, pull-based data plane ✅

## 9. Open architecture questions

1. Change detection: fanotify vs inotify + periodic reconcile scan — decided approach: inotify + periodic scan (spec open Q1).
2. Journal format and embedded store backend — now: Raft log + BoltDB stable store (hashicorp/raft).
3. Transport mechanism — pull-based from leader's journal (to be wired).
4. Batching / backpressure — spec open Q2.
5. Quarantine layout: `.dirsync/quarantine/epoch-N/`.
6. Checksum / integrity — spec open Q8.

## 10. Next steps

- Mode comparison, failure-state model, Go package layout, MVP plan with explicit exclusions.
