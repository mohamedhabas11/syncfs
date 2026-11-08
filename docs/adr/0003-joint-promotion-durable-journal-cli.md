# ADR 0003 — Joint promotion, durable journal, CLI validation

**Status:** Accepted (2026-11-08)

## Context

M4-02 claimed "joint-consensus promotion" but only emitted single
`ConfChange AddNode` triples; M3-03's journal was memory-only so a client
restart forgot unknown ops; the M4-03 CLI printed a stub line and exited 0
for any arguments.

## Decision

- membership: add `RequestJointPromotion` (ConfChangeV2 JointImplicit) +
  `LeaveJoint` (empty ConfChangeV2) and `BeginDrain`. Single-change
  `RequestPromotion` kept for the simple path; joint is the replacement
  path for exactly-3 voters.
- client: make the journal durable (atomic `journal.json` rewrite +
  fsync dir + load on open). `Reconnect` now actually invokes the server
  lookup and folds results back.
- CLI: `admin bootstrap|status|volume|member`, `daemon --config`,
  `mount --volume/--target` validate required flags/files/dirs and exit
  2 (usage) / 1 (missing file or dir). Control-plane proposals stay with
  the daemon; the CLI never silently succeeds.

## Consequences

- Joint flow: Learner -Catchup-> Joint -LeaveJoint-> Promoted, then
  Draining -> Removed. `TestJointPromotionFlow` covers gates + draining.
- Journal durability covers restart only (`journal.json` in the client dir);
  encryption remains LUKS (HUMAN_TODO).
- CLI still does not propose to Raft by itself — it validates and describes
  the control-plane operation (see `docs/reference/cli.md`).
