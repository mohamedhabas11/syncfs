> **ARCHIVED (2026-11-08) — superseded as MVP decision by decision D-001 (research/07-design-fork-reconciliation.md §3.2, §3.5).** hashicorp/raft MVP selection, per-directory log-index epoch fencing, and pull-based steady-state transport are superseded. MVP consensus is etcd/raft/v3 per volume with Raft term/index fencing and QUIC push/pipeline transport (.spec/technical_spec.md §§6, 8.1, 8.4). Retained as reference for post-MVP bounded-loss async tiers where hashicorp/raft's FSM/NotifyCh ergonomics may be revisited with a written invariants-translation proof.

# Consensus Layer Decision — Directory Replication Daemon

**Status:** Decided (v0.1)
**Date:** 2026-11-08
**Related:** `.spec/directory-replication-daemon.md`, `arch/architecture-proposal.md`

## Context

- Synchronous / strongly-coordinated replication was rejected: inotify-style change detection can't block or reject writes that already landed.
- Adopted alternative: single-writer leases with fencing (FUSE rejected as not "native" enough).

## Decision

Adopt **`hashicorp/raft`** as the consensus layer.

## Decision details

1. **Topology:** single Raft group, cluster-wide. Per-directory leases are stored as state-machine data.
2. **Voters / witness / learners:**
   - Witness: voter whose log holds only small metadata; file data never goes through Raft.
   - Remote/geographic nodes: join as non-voters (`AddNonvoter`) and follow asynchronously.
3. **Self-fencing:** leader lease timeout → step down if quorum unreachable → notify via leadership channel → trigger fence (read-only, stop accepting writes).
4. **Fencing token:** per-directory log index of the lease-grant entry = epoch; Raft term is cluster-wide, per-directory token gives the right granularity; replicas reject older tokens.

## Library boundaries

**Provides:**

- Leader election
- WAL
- Log replication
- Leader lease timeout
- Leadership-channel notification

**Does not provide:**

- Application-level read-only fence after leadership loss — `remount,ro` may fail with EBUSY if a writer holds the mount open; fallback (freeze/kill open-file holders) required; `ExecStopPost` is best-effort only.
- Catch-up, quarantine, conflict handling, POSIX apply — all above the library.

**Operator recovery** (lost quorum, wrong peer removed) remains a manual procedure.

## Consequences

- Resolves the "no external or server database" constraint via embedded Raft log + stable store in a private state directory (outside managed dirs).
- Clarifies Raft term vs per-directory epoch distinction.

## Open items

- Lease-grant record format and lease TTL policy.
- Transport wiring (pull-based from leader's journal).
- HashiCorp Raft version pin.
- Batching / backpressure (engineering Q2).

## Next

- Mode comparison spec sheet.
- Failure-state model spec sheet.
- Go package layout.
- MVP plan with explicit exclusions.
