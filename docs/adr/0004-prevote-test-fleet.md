# ADR 0004 — Disable PreVote for VM test fleet

**Status:** Accepted (2026-11-09) for v0.1-test fleet only. **Not** for production.

## Context

Spec §8.4 requires `PreVote: true`. In the 3-process QUIC test harness,
simultaneous PreVote campaigns deadlock (each voter also pre-candidate,
denying others; `TestThreeDaemonElection` sends grants but no leader after
15s). Sequential VM startup plus disabled PreVote elects deterministically
with `CheckQuorum: true` still enforced.

## Decision

For the VM test fleet milestone, `driver` sets `PreVote: false`. Production
rollout must re-enable `PreVote: true` per spec and re-verify election + a
partitioned-voter disruption test before any production claim. This ADR
deviation is recorded in `docs/spec/status.md` and `docs/known-limitations.md`.

## Consequences

- Test VMs elect without PreVote; production remains spec-required.
- No perf/safety claim is made from test-fleet election alone.
