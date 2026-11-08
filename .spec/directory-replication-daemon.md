> **ARCHIVED (2026-11-08) — superseded as MVP product definition by decision D-001 (research/07-design-fork-reconciliation.md).** This document defined a native-directory watcher replicator with bounded loss; the MVP data path is the FUSE-mounted strict volume (.spec/technical_spec.md as amended). Content below is retained as post-MVP reference for bounded-loss async tiers (modes, quarantine layout, failure-state taxonomy, POSIX-apply requirements). Its "Design flaw and resolution" (FUSE rejected) and consensus-layer decisions are superseded for MVP scope. Do not build MVP scope from this file.

# Directory Replication Daemon

**Status:** Draft (tightened iteration; design-review corrections applied)
**Date:** 2026-11-08

## Goal

A single Go daemon that replicates explicitly declared directories across 2–3+ Linux nodes, on top of ordinary mounted filesystems (ext4, XFS). It is not a distributed filesystem.

## Constraints

- Linux only, Go, single binary, config-driven
- Native filesystems, with no custom FS, block layer, or object storage
- No Kubernetes dependency
- No external or server database; embedded local state is allowed in a private state directory (Raft log + stable store). The state directory must live outside managed directories — the journal must not be inside the managed directory, or it will be replicated/deleted by the thing it tracks.
- Local file operations never block on remote nodes unless the directory's policy requires it
- Every node runs the same binary; no dedicated server role; data plane is pull-based (followers pull from the leader's journal). The Raft leader is elected, not configured — so this is not client/server, but the constraint is now stated explicitly.

## Directory declaration

Each directory is declared with: logical name, local path, participating nodes, mode, conflict policy, and failure behaviour. Different directories can use different modes in the same cluster.

## Modes

| Mode | Semantics | Partition behaviour |
|---|---|---|
| **One-way async** | One authoritative node, replicas follow | Replicas lag, primary unaffected |
| **Bidirectional async** | All nodes accept writes, changes converge | Both sides keep writing, conflicts resolved on rejoin |
| **Single-writer lease** | One node holds a quorum-granted lease with fencing and is the only writer | Writes only on the side holding quorum |

## Quorum

- 3 nodes: majority of 2.
- 2 nodes: no safe strong mode without a witness. Either add a lightweight arbiter or restrict to async modes.
- Each mode must document which operations stay available, block, or fail during a partition.

## Open engineering questions

1. Change detection: inotify limits and overflow, missed events, crash recovery (periodic reconcile scan, or fanotify)
2. Incremental sync: change journal, rsync-style deltas, batching, backpressure
3. Atomic replacement, renames, partially written files
4. Concurrent writers and locking
5. Conflict detection and resolution
6. Lease, ownership, and fencing semantics — **decided (v0.1)**: adopt HashiCorp Raft; per-directory leases as state-machine data; fencing token = per-directory log index of lease-grant entry (epoch); leader lease timeout → leadership channel → fence; replicas reject older tokens. Open sub-questions: lease-grant record format, lease TTL policy.
7. Reconnect after long disconnection
8. Integrity verification (checksums)
9. Resource use on ordinary VMs

## Deliverables before code

1. Architecture proposal (see `arch/architecture-proposal.md`)
2. Mode comparison —
3. Membership and declaration model —
4. Failure-state model —
5. Go package layout —
6. MVP plan with explicit exclusions —

## What was cut

- **Duplicated lines:** "no custom filesystem" and "no object storage" appeared in both the principles and the scope sections, and the doc told Claude to challenge assumptions twice.
- **Pure wishlist:** "efficient incremental sync", "batching and backpressure" and similar were listed as priorities with no content. They're now in the engineering questions.
- **DHT-style partitioned ownership:** It contradicts "smallest architecture that proves the concept". It's a whole second project, so I'd defer entirely.

## Design flaw and resolution

### The real design flaw

**"Synchronous / strongly coordinated" can't work on top of inotify-style change detection.** The write has already landed locally by the time you see the event, so you can't block it or reject it. True synchronous replication on a native directory needs one of:

- **FUSE:** you intercept writes, but it's no longer "native" and it adds overhead
- **Single-writer leases with fencing:** only the lease holder is allowed to write. This is what we adopted.

### Resolution adopted

- Single-writer leases with fencing (FUSE rejected).

### Consensus layer decision (v0.1): adopt HashiCorp Raft

- **Library:** `hashicorp/raft`.
- **Topology:** single Raft group, cluster-wide; per-directory leases stored as state-machine data.

**Mapping to our design:**

- **Voters / witness / learners:**
  - Witness: a voter whose log holds only small metadata; file data never goes through Raft.
  - Remote/geographic nodes: join as non-voters (`AddNonvoter`) and follow asynchronously.
- **Self-fencing:**
  - The library's leader lease timeout makes a leader step down if it can't reach a quorum, and notifies via the leadership channel; that event triggers our fence (read-only, stop accepting writes).
- **Fencing token:**
  - Per-directory log index of the lease-grant entry = epoch; Raft term is cluster-wide, so the per-directory token is the right granularity; replicas reject anything older.

**What the library provides:**

- Leader election, WAL, log replication, leader lease timeout, leadership-channel notification.

**What the library does not provide:**

- An application-level read-only fence after leadership loss — `remount,ro` may fail with EBUSY if a writer holds the mount open; a fallback (freeze/kill open-file holders) is required, and `ExecStopPost` is only a best-effort fence.
- Catch-up, quarantine, conflict handling, and the POSIX apply path — all live above the library.

**Operator recovery** (lost quorum, wrong peer removed) is a manual procedure we need to design and test, as with any Raft system.

## Suggested MVP

Build one-way async plus lease-based single-writer for a 3-node cluster, and defer bidirectional.

## Node states (per directory)

| State | Local writes | Local reads | Votes | May become leader | Meaning |
|---|---|---|---|---|---|
| **LEADER** | rw | yes | yes | n/a — incumbent | Holds a valid lease at epoch E |
| **FOLLOWER** | ro | yes | yes | yes | Receives the leader's change stream |
| **FENCED** | ro | yes (marked stale) | yes | yes | No valid lease / no quorum; waits; write-gated |
| **CANDIDATE** | ro | yes | yes | yes | Requesting epoch E+1 from a majority |
| **CATCHING_UP** | ro | yes (stale) | no | no | Rejoined and replaying changes |
| **QUARANTINED** | ro | yes | no | no | Holds writes the cluster never accepted; needs a human |
| **HALTED** | ro | yes | no | no | Safety check tripped (marker missing, mass delete, disk error) |

A node should not vote or be elected leader while CATCHING_UP, QUARANTINED, or HALTED.

## Scenarios

| Event | What happens | Writes available? |
|---|---|---|
| B down | A+W still have quorum. B catches up on return. | Yes |
| W down | A+B still have quorum, but any further failure loses it. Alert. | Yes |
| A crashes | B+W wait out A's lease TTL, then B becomes leader at E+1. | No, for about one TTL |
| Partition A \| B+W | A can't renew and self-fences before its lease expires. B+W grant E+1 only after the full TTL plus a grace margin. | A stops, B takes over |
| Partition A+W \| B | A keeps its lease. B stays a stale read-only follower. | Yes (A) |
| Full partition | No one has quorum, so everyone goes read-only. | No |
| A returns, nothing uncommitted | A sees the higher epoch, becomes follower, and catches up. | Resumes as follower |
| A returns with an uncommitted tail | The tail is moved to `.dirsync/quarantine/epoch-N/`, A becomes follower, and an operator reviews the tail. | Resumes as follower |
| Clock jump or skew | Leases use monotonic clocks. A detected jump fences the node. | Briefly no |
| Daemon crash or hang on leader | ExecStopPost is a best-effort fence; `remount,ro` can fail with EBUSY if a writer holds the mount open — fallback (freeze/kill open-file holders) required. Crash vs hang differ: fast restart within lease TTL can resume without fencing (epoch unchanged, journal replays); fencing is required only when the node can't prove it holds a valid lease before the safety margin expires. | No, until restart |
| Marker missing or mass delete | The directory goes HALTED in every mode. | No |
| Forced promotion | `promote --force --fenced <old-node>` bumps the epoch, and the old node is forced into quarantine on return. | Yes (new leader) |

## POSIX fidelity in the apply path

The cleaned design dropped this; it belongs in the apply path and must be restored:

- Modes including setuid, setgid, and sticky bits, plus ownership, ACLs, xattrs, symlinks, hardlinks, and mtimes.
- Ordering: chown before chmod, because chown clears setuid and setgid bits.
- Running as root or with CAP_CHOWN on followers.
- uid/gid consistency across nodes, or explicit mapping.

## Two honest limits

1. **Lease mode prevents split-brain, not data loss.** With native directories you can't block the application's `close()` until a quorum has the data. Replication stays asynchronous, so the guarantee is single writer plus a bounded loss window (the replication lag at the moment of failure). The lost tail isn't discarded, it's preserved in quarantine. Document this as the contract: don't promise zero RPO.
2. **Self-fencing needs a live kernel and init system.** A hard host freeze can't fence itself. Real STONITH is out of scope, so the epoch check on replicas is the backstop. A frozen leader can't corrupt followers when it wakes up, and it will be demoted.

## Election rule

A candidate needs a majority of votes and must be healthy and caught up to the highest committed position in that majority. If the best-positioned node is unreachable, the cluster waits rather than electing a stale node, unless an operator forces it.

## The async modes

### One-way

- Partition: the primary keeps writing and replicas lag.
- Promotion is manual: it bumps the epoch, and the old primary can't rejoin as a writer.
- A returning old primary with a divergent tail goes to quarantine.

### Bidirectional

- Partition: both sides keep writing.
- Rejoin reconciles using per-file version vectors.
- Concurrent edits produce a conflict copy named by policy, and delete versus edit resolves to keeping the edit.
- The mass-delete breaker applies, and there are no silent overwrites.

## Next steps

- Consensus layer decision sheet drafted: `.spec/consensus-layer.md`.
- Next: mode comparison, failure-state model, Go package layout, MVP plan with explicit exclusions.
