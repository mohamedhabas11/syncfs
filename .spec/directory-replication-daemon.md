# Directory Replication Daemon

> **Superseded exploratory draft.** This document was the useful precursor to
> the current design, but its asynchronous native-directory model cannot meet
> the requested strong shared-directory contract. The active specification is
> [`technical_spec.md`](technical_spec.md); this file is historical context only.

**Status:** Draft (tightened iteration)
**Date:** 2026-11-08

## Goal

A single Go daemon that replicates explicitly declared directories across 2–3+ Linux nodes, on top of ordinary mounted filesystems (ext4, XFS). It is not a distributed filesystem.

## Constraints

- Linux only, Go, single binary, config-driven
- Native filesystems, with no custom FS, block layer, or object storage
- No Kubernetes dependency
- Local state (change journal, peer state) in an embedded store, not an external database
- Local file operations never block on remote nodes unless the directory's policy requires it

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
6. Lease, ownership, and fencing semantics
7. Reconnect after long disconnection
8. Integrity verification (checksums)
9. Resource use on ordinary VMs

## Deliverables before code

1. Architecture proposal
2. Mode comparison
3. Membership and declaration model
4. Failure-state model
5. Go package layout
6. MVP plan with explicit exclusions

## What was cut

- **Duplicated lines:** "no custom filesystem" and "no object storage" appeared in both the principles and the scope sections, and the doc told Claude to challenge assumptions twice.
- **Pure wishlist:** "efficient incremental sync", "batching and backpressure" and similar were listed as priorities with no content. They're now in the engineering questions.
- **DHT-style partitioned ownership:** It contradicts "smallest architecture that proves the concept". It's a whole second project, so I'd defer entirely.

## The real design flaw

**"Synchronous / strongly coordinated" can't work on top of inotify-style change detection.** The write has already landed locally by the time you see the event, so you can't block it or reject it. True synchronous replication on a native directory needs one of:

- **FUSE:** you intercept writes, but it's no longer "native" and it adds overhead
- **Single-writer leases with fencing:** only the lease holder is allowed to write. This is what replaced it, and it fits your no-custom-FS rule

Also, bidirectional async with conflict resolution is basically Syncthing. The differentiator here is the per-directory declarative policy plus leases, not the sync itself.

## Suggested MVP

Build one-way async plus lease-based single-writer for a 3-node cluster, and defer bidirectional. That covers the cases where correctness matters and keeps the conflict-resolution rabbit hole out of v1.

## Node states (per directory)

| State | Local writes | Local reads | Votes | Meaning |
|---|---|---|---|---|
| **LEADER** | rw | yes | yes | Holds a valid lease at epoch E |
| **FOLLOWER** | ro | yes | yes | Receives the leader's change stream |
| **FENCED** | ro | yes (marked stale) | yes | No valid lease, no quorum; waits |
| **CANDIDATE** | ro | yes | yes | Requesting epoch E+1 from a majority |
| **CATCHING_UP** | ro | yes (stale) | no | Rejoined and replaying changes |
| **QUARANTINED** | ro | yes | no | Holds writes the cluster never accepted; needs a human |
| **HALTED** | ro | yes | no | Safety check tripped (marker missing, mass delete, disk error) |

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
| Daemon crash or hang on leader | `ExecStopPost` remounts the directory read-only, the watchdog kills a hung daemon, and the mount starts read-only until the daemon grants rw. | No, until restart |
| Marker missing or mass delete | The directory goes HALTED in every mode. | No |
| Forced promotion | `promote --force --fenced <old-node>` bumps the epoch, and the old node is forced into quarantine on return. | Yes (new leader) |

## Two honest limits

1. **Lease mode prevents split-brain, not data loss.** With native directories you can't block the application's `close()` until a quorum has the data. Replication stays asynchronous, so the guarantee is single writer plus a bounded loss window (the replication lag at the moment of failure). The lost tail isn't discarded, it's preserved in quarantine. Document this as the contract: don't promise zero RPO.
2. **Self-fencing needs a live kernel and init system.** A hard host freeze can't fence itself. Real STONITH is out of scope, so the epoch check on replicas is your backstop. A frozen leader can't corrupt followers when it wakes up, and it will be demoted.

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

- Sketch the Go package layout and MVP plan, or write the replication protocol (change journal, committed position, epoch checks).
