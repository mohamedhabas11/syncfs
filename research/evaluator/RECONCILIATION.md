# Evaluator feedback — reconciliation (2026-11-09)

**Inputs:** `research/evaluator/feedback.md` (12 findings F-01..F-12 + security
blockers) and `research/evaluator/syncfs-production-readiness-task-sheet.md`
(T00..T13) against `main` at `828acac` (PARTIAL M4-03/Obs) + `3a5fd48`
(PARTIAL M3-02). **Method:** source inspection of every cited file
(`internal/fusefs/{fusefs,posix,xattr}.go`, `cmd/syncfs/main.go`,
`docs/known-limitations.md`, `docs/spec/status.md`, `.spec/{tasks,roadmap}.md`,
`docs/posix-compat.md`, `docs/reference/cli.md`, `internal/{transport,auth,
volume,metadata,observability}/*`, `.github/workflows/ci.yml`,
`deploy/systemd/*`) plus recent commit diffs. Go tests could not run on the
evaluator host or this dev host (Go 1.15.6 < `go 1.26.0` in `go.mod`); verdicts
are code-evidence based and marked as such, consistent with the review
limitation disclosed in `feedback.md` §9.

## Executive verdict

**The feedback is substantially correct and the “not production-ready /
qualification incomplete” verdict stands.** No P0 finding was refuted. Every
P0 code claim was confirmed in source. Three sub-claims were partially stale
against the two most recent commits (Lookup/Readdir and `/metrics` serving
had landed), and the enrollment race was narrower than stated for
`MemoryStore` (still a genuine API-level TOCTOU). The task sheet’s scope
assumption (keep the published FUSE/Raft product contract, no watcher/Gluster
hybrid) matches `README.md` + `.spec/technical_spec.md` and is accepted;
historical watcher docs in `arch/` / `docs/directory-daemon.md` are already
labeled superseded in the README and remain so.

**This commit closes no P0 task by itself.** It fixes the cheap,
high-confidence correctness and honesty gaps the feedback is right about, and
leaves the large T02/T03/T04/T06/T07/T08/T12 runtimes explicitly pending.
Production readiness still requires the full task-sheet sequence
(T00–T13, Gates 1–4).

## Finding-by-finding verdict

| ID | Verdict | Evidence in this repo |
| --- | --- | --- |
| F-01 No production data path (`NewStrictHandler` no coordinator) | **CONFIRMED** | `cmd/syncfs/main.go:mountOnce` builds `NewStrictHandler` and calls `KernelMount` with no `SetSyncer`. `Syncer` covered only `OnWrite`; namespace ops never called it. Strict `WriteFile` correctly returns EIO (good — preserved). |
| F-02 Fsync not durable (`fileHandle.Fsync` / `Handler.Fsync` no-op) | **CONFIRMED** | `Handler.Fsync` only bumped `mtime`; `fileHandle.Fsync` returned 0 after a released-check. `Truncate` also mutated memory with no sync call. Same defect class as F-01: false durable receipt. |
| F-03 Incomplete tree (flat-root only) | **CONFIRMED (narrowed)** | `posix.go` still does every op on `h.root.children`; no `a/b/c` path resolution. `3a5fd48` added `childNode` + `Root.Readdir` for root children, so “stat-root only” is now **depth-one only**, not zero — nested traversal remains pending (#15). |
| F-04 No functional daemon (`runDaemon` metrics-only) | **CONFIRMED (wording superseded)** | `828acac` made `runDaemon` parse JSON, validate `quorum`, and serve `/metrics`+`/healthz` via `serveDaemon`; previous “validate-and-print stub” is gone. Still not a Raft/storage service — no WAL/snapshot/chunk/Raft/QUIC wiring. T02 pending. |
| F-05 Admin ops are stubs | **CONFIRMED** | `runBootstrap/runStatus/runVolume/runMember` still `fmt.Fprintln` the intended action. No client → daemon calls exist. |
| F-06 Metadata durability incomplete | **CONFIRMED** | `metadata.StateMachine` is in-memory maps; #6 still reads “in-memory scaffold; coordinator lands with M5”. `volume.Coordinator` exists and is tested but is not mounted on any persistent backend in the daemon path. |
| Security: enrollment Get-then-Mark race | **PARTIALLY OVERSTATED, API RISK CONFIRMED** | `Consume` = `Get` + checks + `MarkConsumed`. `MemoryStore.MarkConsumed` re-checks `ConsumedAt` under the store mutex, so two concurrent `Consume` calls on `MemoryStore` already yield exactly one winner. The race is real at the **interface** level: a generic store with no atomic check-and-burn can double-consume. Fixed by adding `AtomicEnrollmentStore.TryConsume` (single-lock) and delegating `Consume` to it. |
| Security: opener role not auto-checked | **CONFIRMED** | `OpenClassifiedStream(conn, class)` only set priority; comment said “callers MUST validate”. Acceptor side (`AcceptClassifiedStream`) did validate. QUIC `ConnectionState` exposes only the peer cert, so the opener cannot derive its own identity from the conn — the fix requires passing the local identity. Fixed (breaking signature change, one caller). |
| F-07 Snapshot/GC partial | **CONFIRMED** | `status.md` M4-01 PARTIAL; `known-limitations` #6/#7 note caller composition + durable compaction pending. `ComposeAndSweep`/`WriteSnapshotFileWithIDs` landed incrementally, leaving integration pending. |
| F-08 Membership partial | **CONFIRMED** | M4-02 PARTIAL; “ConfState driver still convention” text unchanged. Pending-held + `Quorum()` gains are incremental, not joint-consensus wiring. |
| F-09 Fault harness incomplete | **CONFIRMED** | `testkit/cluster` is an in-memory mesh with `SetFaults`; `status.md` explicitly defers Docker/process realization to M5. No `tc netem` / three-process suite exists. |
| F-10 Performance unqualified | **CONFIRMED** | `docs/benchmarks/README.md` “nothing about §11 gates”; `status.md` §11 rows UNVERIFIED. No QUIC/FUSE/10 GbE path measured. |
| F-11 Target apps unqualified | **CONFIRMED** | M5-02 DEFERRED under ADR-0001; no target app/version named. |
| F-12 Observability not wired | **CONFIRMED** | `internal/observability` collector + `Handler` exist and are now served (`828acac`), but `runDaemon` used a constant `quorumFn() int { return 1 }` and `SetFromStats` seeded from config. Metrics are still static until T02 wires live Raft/coordinator state. This commit wires the configured quorum into `/healthz` and documents it as liveness, not readiness. |
| Doc hygiene (tracker contradictions, E-13) | **CONFIRMED** | `.spec/tasks.md` said “no implementation code exists yet” + blocked phases while checking M2-05/M3-02/M4-01..04 done; `roadmap.md` said M1–M4 implemented; `status.md` said PARTIAL for the same items. Fixed in this commit. |
| CLI docs staleness (E-09) | **CONFIRMED (both directions)** | `docs/reference/cli.md` called daemon/mount “STUB … does not start/mount” while code (post-`828acac`) serves HTTP and calls `KernelMount`. Admin section was correct; daemon/mount section was inverted. Fixed. |
| Systemd hardening (E-15) | **CONFIRMED** | Units had hardening only as comments (`ProtectSystem=…` in a comment line). Converted to active directives in this commit. |
| CI gates (E-14) | **CONFIRMED** | CI ran only verify/vet/test/race + a conditional FUSE skip; `Makefile` `lint`/`proto-*` targets (buf lint/breaking, `go build`, `gofmt`) were not enforced in CI. Added build + gofmt + buf lint/breaking + required FUSE gate. |
| Scope discrepancy (Gluster/watcher vs FUSE/Raft) | **CONFIRMED as process point** | The task sheet correctly requires picking one contract. The repo already declares FUSE/Raft normative (`technical_spec.md` + README) and the watcher docs superseded. Accepted; no dual-model work. T00 remains to freeze the exact deployment profile. |

## Task-sheet verdict (T00..T13)

| Task | Verdict | Action in this commit |
| --- | --- | --- |
| T00 Lock contract | Pending (docs already point at `technical_spec.md`; exact deployment profile still missing) | None (needs human sign-off on distro/kernel/app/profile) |
| T01 Reconcile docs | **Closed as honesty fix** (not the full gate) | Fixed `.spec/tasks.md` header/phase table + 6 PARTIAL checkboxes, `.spec/roadmap.md`, `README` status line, `docs/reference/cli.md`, `docs/posix-compat.md`, `docs/known-limitations.md` (#10/#15/#17), `internal/observability` health comment. |
| T02 Daemon runtime | **Not started** (still metrics/health only) | Small honesty hardening only: `quorum < 2` rejected, startup log states metrics-only, `/healthz` wired to configured quorum and documented as liveness. No storage runtime claimed. |
| T03 Durable writes | **Incrementally improved, not done** | `Syncer` now covers Write/Truncate/Fsync/FsyncDir; `Truncate`/`Fsync`/`FsyncDir`/handle-fsync fail closed (EIO + rollback) without a coordinator. Namespace mutations still local-only — explicitly documented as pending. Mount still has no coordinator (fail-closed EIO), which the feedback correctly says to preserve. |
| T04 Kernel FUSE tree | **Incrementally improved, not done** | `childNode.Open` now carries `name` so handle-fsync is bound to a child; depth-one `Lookup`/`Readdir` already landed in `3a5fd48`. Nested traversal, `Forget`, cross-dir rename, inode-ID binding still pending. |
| T05 AuthZ gaps | **Narrowly closed for the two named gaps** | Enrollment: `AtomicEnrollmentStore.TryConsume` + `Consume` delegation + concurrent single-winner test. Transport: `OpenClassifiedStream(conn, self, class)` enforces opener role inside the API + denied-opener test. Broader T05 items (rotation, audit, downgrade/0-RTT tests) remain open. |
| T06 Metadata/snapshot/GC | Not started | None beyond #6/#7 honesty (already accurate). |
| T07 Membership lifecycle | Not started | None beyond status honesty (already accurate). |
| T08 Real fault tests | Not started | None (in-memory mesh remains the only evidence; unchanged). |
| T09 Observability/runbooks | Not started | Liveness-vs-readiness honesty only; no live wiring, no runbooks added. |
| T10 Systemd hardening | **Partially closed** | Active `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`, `ReadWritePaths`, kernel/control-group and address-family restrictions added to both units, plus `User`/`Group` + install pre-steps documented. `systemd-analyze verify/security` still needs a Linux host run. |
| T11 CI gates | **Partially closed** | Added `go build ./...`, `gofmt -l -s` fail-fast, `buf lint` + `buf breaking --against api/baseline`, and made the FUSE gate required (`go test -run TestFUSE` with no skip). Branch-protection / SBOM / signing / scheduled-fault still open. |
| T12 Perf + target app | Not started | None. Targets untouched (no “move the goalposts”). |
| T13 Upgrade/rollback/recovery | Not started | None. |

## Code changes in this reconciliation

- `internal/fusefs/xattr.go` — `Syncer` gains `OnTruncate`/`OnFsync`/`OnFsyncDir`; `SetSyncer(nil)` no longer silently installs `NoopSyncer` (nil = fail-closed).
- `internal/fusefs/posix.go` — `Truncate`/`Fsync`/`FsyncDir` route through `Syncer`, roll back + `EIO` on nil/error.
- `internal/fusefs/fusefs.go` — `fileHandle` carries `name`; `Fsync` waits on `OnFsync`/`OnFsyncDir`, `EIO` without a coordinator; `childNode.Open` propagates caller+name.
- `internal/fusefs/{xattr_test,m3_01_test}.go` — fakes implement the extended seam; `TestSyncerSeamIsCalled` now asserts nil ⇒ `EIO`.
- `internal/fusefs/fsync_test.go` (new) — `TestFsyncTruncateFailClosed`, `TestFsyncTruncateCoordinatorError`, `TestHandleFsyncFailClosed`.
- `internal/auth/enrollment.go` — `AtomicEnrollmentStore.TryConsume` (single-lock); `Consume` delegates; generic Get-then-Mark documented as non-atomic.
- `internal/auth/enrollment_race_test.go` (new) — `TestConcurrentConsumeExactlyOneSucceeds` (16 goroutines, exactly 1 win).
- `internal/transport/framing.go` — `OpenClassifiedStream(conn, self, class)` validates `self` before opening.
- `internal/transport/{role_test,session_test}.go` — denied-opener test; caller updated to pass the local identity.
- `cmd/syncfs/main.go` — daemon rejects `quorum < 2`, logs metrics-only, `serveDaemon(..., quorum)` wired to `/healthz`.
- `cmd/syncfs/main_test.go` — quorum-1 rejection test.
- `.github/workflows/ci.yml` — build + gofmt + buf lint/breaking + required FUSE gate.
- `deploy/systemd/*.service` — active hardening directives.
- Docs — `.spec/tasks.md`, `.spec/roadmap.md`, `README.md`, `docs/reference/cli.md`, `docs/posix-compat.md`, `docs/known-limitations.md`.

Deliberately **not** done: T02 storage runtime, full T03 coordinator mount
wiring (mount intentionally stays fail-closed EIO rather than gaining a
fake-healthy Noop path), T04 nested tree, T06/T07 integration, T08 Docker
faults, T09–T13 qualification. Each remains listed as PARTIAL/pending in
`docs/spec/status.md` and `docs/known-limitations.md` — no DONE claim was
added in this commit.

## What to do next (recommended sequence)

1. T02: replace the metrics-only daemon with the real composition root
   (WAL/snapshot/chunk/metadata/Raft/QUIC/coordinator/membership/GC), with a
   three-process smoke before any FUSE work.
2. T03+T06 together (interface freeze first): wire the volume coordinator as
   the `Syncer` implementation and route every mutation (not just
   write/truncate/fsync) through it; land durable snapshot/compaction.
3. T04 against that coordinator: bind the Handler to metadata inode IDs so
   nested `a/b/c` + cross-directory rename work through the kernel.
4. T05 remainder + T07 on the stable interfaces; then T08 real-process faults.
5. T09–T13 only after Gates 1–2 pass; do not move §11 targets to fit
   current measurements.
