# Synthesis: Phase-0 Closure (2026-11-08)

All seven domain agents have reported. This file indexes the reports, validates their honesty, records what changed, and states the one remaining gate.

## Report index

| Agent | File | Domain | Verdict summary |
|---|---|---|---|
| 7 | `07-design-fork-reconciliation.md` | Fork (FUSE vs native-dir) | **Fork closed (D-001).** Watcher can never deliver quorum-durable-before-ack (inotify/fanotify post-hoc, lossy, racy, mmap-blind; close completes locally). FUSE+etcd/raft/v3 is the only camp that meets the north star. Camp B operability folded in; async modes deferred. |
| 1 | `01-consensus-raft.md` | Consensus/Raft | Ready/Advance **VERIFIED**, §8.4.4 wording **CONTRADICTED** (fixed). Joint → simple change (fixed). ReadIndex needs `ReadOnlySafe` (fixed). 8 gaps G-1–G-8. |
| 2 | `02-fuse-posix.md` | FUSE/POSIX | Direction sound, precision not. Lock families conflated, `EHOSTDOWN`/`ESTALE`/`O_DIRECT` errors wrong (fixed), suspension guarantee is *block-not-serve*, 17 corrections C-01–C-17. |
| 3 | `03-transport-security.md` | QUIC/TLS/PKI | QUIC HOL sound narrowly; priority is local-only (fixed); SAN identity profile missing (fixed); Go 1.25 stale → 1.26 (fixed). 11 gaps G1–G11. |
| 4 | `04-storage-model.md` | Pebble/BLAKE3/chunks/GC | Pebble/BLAKE3/64 KiB defensible. Pebble `Sync` policy, snapshot envelope, GC epoch grace missing (fixed). 8 gaps G1–G8. |
| 5 | `05-protocol-perf.md` | Protobuf/idempotency/perf | Perf targets unproven hypotheses with honest math. Idempotency retention bound, deadline owner, group-commit owner missing (fixed). 10 gaps G1–G10. |
| 6 | `06-ops-phase0-gate.md` | Ops/Phase-0 gate | Observability insufficient, 7 join interlocks missing, fault matrix 10 cases narrow, 28-checkbox falsifiable gate. 8 `done/passed` claims lack evidence. |

## Honesty validation

- Each agent was instructed to touch only its own `research/` file. `git status` before edits showed only pre-existing dirty state (`.spec/directory-replication-daemon.md`, `arch/architecture-proposal.md` modified; `.spec/consensus-layer.md` untracked) plus `research/` — **no agent edited specs or docs**, consistent with tasking.
- Each report owns its limits explicitly (Agent 1: paper PDF unfetchable, no fabricated quotes; Agent 7: Q4 entry-point citations; Agent 2: no mounts/benchmarks, master-drift; Agents 3–5: version-drift/truncation risks). No silent upgrades from "estimate" to "fact" were found; Agent 5's latency math is shown in full.
- Independent cross-checks agree where they overlap (Go version drift: Agents 3+6; joint-consensus wording: Agent 1 agrees with Agent 7's `TransitionAuto` point; quarantine/GC: Agents 1+4). No agent contradicts another agent's primary-source verdict.

## What changed (this patch)

1. **Decisions:** `.spec/decisions.md` created with D-001 verbatim.
2. **Fork hygiene:** `ARCHIVE` banners prepended to `.spec/directory-replication-daemon.md`, `.spec/consensus-layer.md`, `arch/architecture-proposal.md`; `docs/directory-daemon.md` "Living design spec" contradiction removed. Reconciliation banners added to `.spec/technical_spec.md`, `.spec/research.md`, `docs/architecture.md`.
3. **Technical corrections in `technical_spec.md`:** Go 1.26+ + quic-go ≥ v0.62; SAN URI profile + `VerifyConnection`; QUIC priority mechanism named; §8.4 Advance wording + Raft Config knobs; ReadIndex/`ReadOnlySafe` + three-lease separation; Pebble `Sync` rationale; snapshot envelope + GC epoch grace; lock-family separation; FUSE negative-cache/`Forget`/`INC_EPOCH`; OperationID retention bound + deadline owner; group-commit owner; pack record format + dir-fsync; §11 accounting + flush-honoring rule; §14 defaults; xattr/ACL encoding admission.
4. **Error fixes in `docs/compatibility.md`:** `EHOSTDOWN` dropped, `ESTALE` trigger defined, `O_DIRECT` → `EINVAL`, `EDEADLK` added, `ENOSYS`/`EOPNOTSUPP` table required.
5. **Honesty downgrade in `.spec/tasks.md`:** see Final Verification note below — Foundation stays *done*, but "passed" claims without hash/log/URL are explicitly marked pending gate evidence.

## The Phase-0 gate (what "closed" means now)

Phase 0 (Foundation) is **ready to close pending gate evidence** — not "done" by assertion. The 28-checkbox falsifiable gate in `06-ops-phase0-gate.md` §4 (G1–G6) is now the closure definition. This patch completes: doc hygiene (banners), D-001, section-level corrections above, and link/diff audits. Remaining for *honest* closure:

- G1 (doc hygiene): banners + link audit — **done in this patch**, verify below.
- G2 (Go baseline): Go 1.26 pins + `go mod verify` — blocked until M1-01 (local Go 1.15.6); must pass on Linux CI.
- G3 (open Q1–Q4 defaults): adopted in §14 — confirm by decision-log entry.
- G4 (observability/enrollment/systemd/fault freeze): corrected in specs; metric catalog + alert table + runbooks remain M4 work — Phase 0 requires only the *frozen* catalog, not running metrics.
- G5 (honesty): Final Verification hash/log/URL — foundation docs pass; Go tests remain Blocked (honest).
- G6 (fork): D-001 + banners — **done in this patch**.

Net: with verification below, Phase 0 closes as "foundation complete, implementation not started." Any remaining unchecked box moves to M1-01's exit criteria, not to a Phase-0 assertion.

## Correction ledger (where each gap went)

| Gap | Home |
|---|---|
| Advance starvation, PreVote/CheckQuorum | technical_spec §8.4 (fixed); full Config in M1-04 |
| Campaign deadlock, vote-vs-campaign | §8.4 (fixed); wrapper in M2-04 |
| Snapshot size, GC epoch | §8.5 (fixed); envelope in M4-01 |
| Lease-revocation bounded wait | §8.2 (fixed as bounded-wait requirement); wait policy in M2-06 |
| Lock families, deadlock, expiry ordering | §8.3 (fixed); wait-queues/fairness in M2-06 |
| Negative cache, Forget, suspension | §8.3 (fixed); table in M3-01 |
| SAN profile, VerifyConnection, 0-RTT | §8.1/§9 (fixed); enrollment state machine in M1-03/M4-02 |
| Priority mechanism | §8.1 (fixed); flow-control numbers in M1-04 |
| Pebble Sync, pack format, dir-fsync | §6/§7.5 (fixed); crash hooks in M2-01/M2-02 |
| Idempotency retention, deadline owner | §8.2 (fixed); journal in M2-05 |
| Group-commit owner | §11 (fixed) |
| `EHOSTDOWN`, `ESTALE`, `O_DIRECT` | compatibility.md (fixed); override table in M3-01 |
| Observability, fault matrix, app contract | technical_spec §12–13 + research/06 §5; implementation in M4–M5 |

No other files were created by this patch. All spec edits were *surgical text replacements*; doubt on any line is resolved against its `research/` report section cited inline.
