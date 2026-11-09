# SyncFS Production-Readiness Task Sheet

**Assessment date:** 2026-11-09  
**Repository:** https://github.com/mohamedhabas11/syncfs  
**Assumption:** This plan keeps the repository's published product contract: a Linux FUSE-mounted, three-voter, strongly consistent shared-directory volume. It does not attempt to turn SyncFS into an asynchronous directory watcher or a general GlusterFS replacement.

## Executive decision

**Do not deploy the current `main` branch for application data.** The repository has useful foundations in protocol, auth, Raft/WAL, chunk storage, metadata, and tests, but it does not yet provide a working end-to-end storage service. The FUSE data path is not wired to durable storage; the strict handler has no production coordinator; `fsync` currently returns success without performing a durable flush; the daemon entry point is metrics-only; administrative commands are stubs; metadata persistence/compaction, membership/GC integration, fault qualification, and workload qualification remain incomplete.

The repository's own ADR-0001 says the release is pre-1.0 and not production-ready, and defers M5 qualification pending reference hardware. Production readiness must therefore mean closing M5 and the code-level gaps below, not merely obtaining green unit tests.

## 1. Evidence register

| ID | Evidence in repository | What it establishes |
|---|---|---|
| E-01 | `README.md`, core contract | Product claims three storage voters, FUSE clients, quorum-durable writes, TLS 1.3/QUIC, learner-first admission. |
| E-02 | `docs/spec/status.md` | M2-05, M3-02, M4-01..04 are partial; M5-01 and M5-02 are deferred; performance targets are unverified. |
| E-03 | `docs/known-limitations.md` #6–#7 | Metadata persistence beyond Raft is an in-memory scaffold; durable WAL compaction is pending; snapshot caller composition/resumable transfer is pending. |
| E-04 | `internal/fusefs/fusefs.go` | `Handler` holds an in-memory tree; `NewHandler` is test-only with `NoopSyncer`; `NewStrictHandler` sets the syncer to nil. |
| E-05 | `internal/fusefs/posix.go` | `Truncate` mutates in-memory state without calling the syncer; `Handler.Fsync` is a no-op. |
| E-06 | `internal/fusefs/fusefs.go` | `fileHandle.Fsync()` and `Flush()` return success after checking only whether the handle was released. |
| E-07 | `docs/posix-compat.md` | FUSE tree is flat/root-only; full persistent inode tree and kernel traversal are pending. Most POSIX tests exercise the in-memory Handler surface. |
| E-08 | `cmd/syncfs/main.go` | `runDaemon` starts a metrics HTTP server with static stats; `runMount` constructs `NewStrictHandler` without a coordinator. |
| E-09 | `cmd/syncfs/main.go`; `docs/reference/cli.md` | Admin commands print intended actions. The CLI reference also incorrectly describes daemon and mount as non-running stubs, while code starts an HTTP server and a FUSE mount shell. |
| E-10 | `docs/known-limitations.md` #10 | Enrollment consumption has a Get-then-Mark race; stream opener role validation remains caller responsibility. |
| E-11 | `docs/spec/status.md` | Snapshot/GC and membership are partial; membership ConfState integration is still convention-based; fault harness Docker realization is deferred. |
| E-12 | `docs/benchmarks/README.md`; `docs/spec/status.md`; ADR-0001 | Published measurements are in-process/Docker VM results, not QUIC/FUSE/10GbE qualification. The specified performance gates are unverified. |
| E-13 | `.spec/tasks.md`, `docs/spec/status.md`, `.spec/roadmap.md` | Tracker state conflicts: task/roadmap checkboxes say done while status records partial; task header and phase table also contain stale “no implementation code” / blocked statements. |
| E-14 | `.github/workflows/ci.yml`; `Makefile` | CI runs module verification, vet, tests, race tests, and a conditional FUSE test. It does not currently make the full fault/qualification fleet a required CI gate; protocol lint/breaking checks are Make targets but not in this workflow. |
| E-15 | `deploy/systemd/syncfs-daemon.service` | Service has basic restart configuration; hardening directives mentioned in comments are not active directives. |

Repository source references:
- https://github.com/mohamedhabas11/syncfs/blob/main/docs/spec/status.md
- https://github.com/mohamedhabas11/syncfs/blob/main/docs/known-limitations.md
- https://github.com/mohamedhabas11/syncfs/blob/main/docs/posix-compat.md
- https://github.com/mohamedhabas11/syncfs/blob/main/internal/fusefs/fusefs.go
- https://github.com/mohamedhabas11/syncfs/blob/main/internal/fusefs/posix.go
- https://github.com/mohamedhabas11/syncfs/blob/main/cmd/syncfs/main.go
- https://github.com/mohamedhabas11/syncfs/blob/main/.github/workflows/ci.yml

## 2. Task execution rules

1. Do not add product features until all P0 tasks pass.
2. Every task must land with code, focused tests, an integration test where relevant, and updated evidence in `docs/spec/status.md`.
3. A unit test of an in-memory Handler is not evidence of kernel-visible POSIX behavior or durable replicated storage.
4. Never return success for `write`, `truncate`, `rename`, `fsync`, `fsyncdir`, lock grant, or admin mutation unless the documented consistency/durability condition has actually been met.
5. Fault tests must assert data invariants and externally visible results, not merely that a process restarts.
6. Any deviation from the technical spec or performance targets requires an ADR with measured evidence and an explicit support-contract change.

## 3. Ordered implementation task sheet

### T00 — Lock the product contract

**Priority:** P0 · **Dependency:** none  
**Evidence:** E-01; `.spec/technical_spec.md`; `.spec/tasks.md` decision log.

**Work**
- Confirm that SyncFS remains a FUSE-based, strongly consistent shared filesystem with three storage voters.
- Keep the explicit exclusions explicit: no WAN, no two-voter mode, no direct backing-directory access, no NFS/SMB gateway, and no writable shared `mmap` in this release.
- Remove or clearly label historical watcher-based documents so they cannot be mistaken for the current implementation contract.
- Define the exact first supported deployment profile: Linux distributions/kernel versions, ext4/XFS versions/options, architecture, node count, client count, file-size/quota limits, and target application/version.

**Exit criteria**
- One approved, current technical contract is linked from README and is authoritative over historical research.
- Every in-scope operation has a defined success/error/durability contract.
- Every excluded operation has an explicit errno/unsupported behavior and a documented user-visible limitation.
- No roadmap item silently widens MVP scope.

### T01 — Reconcile status, task tracker, and documentation

**Priority:** P0 · **Dependency:** T00  
**Evidence:** E-02, E-07, E-09, E-13.

**Work**
- Reconcile `.spec/tasks.md`, `.spec/roadmap.md`, `docs/spec/status.md`, README, `docs/posix-compat.md`, `docs/reference/cli.md`, and `docs/operating-model.md`.
- Remove the stale task header “no implementation code exists yet”, stale phase table, and stale local Go 1.15 handoff note.
- Change checkboxes/statuses to match actual completion. In particular, do not mark M3-02 or M4 tasks fully done when status and limitation registers say partial.
- Correct the CLI reference: daemon currently serves metrics, while mount starts a FUSE shell without a real storage coordinator. Do not describe either as a usable replicated storage service.
- Define evidence format: commit SHA, exact command, environment, test output/report link, and reviewer for every DONE claim.

**Exit criteria**
- No contradictory task/milestone states remain across the repository.
- No command, compatibility, or production claim is stronger than the implementation and test evidence.
- Every DONE item links to reproducible evidence from the current branch.
- Markdown links and docs build pass.

### T02 — Implement the actual storage-daemon runtime

**Priority:** P0 · **Dependency:** T00, T01; protocol/storage foundations.

**Work**
- Replace the metrics-only `runDaemon` path with a real runtime composition root.
- Load and validate a typed configuration containing node/cluster identity, data directory, listen addresses, peer identities/endpoints, CA/cert/key paths, volume definitions, storage limits, and operational timeouts.
- Instantiate and wire WAL, snapshot store, chunk store, metadata state machine, Raft driver, QUIC transport, volume coordinator, membership, repair/GC, and observability.
- Start listeners only after storage recovery and identity validation complete.
- Reject invalid configurations, including an invalid voter/quorum topology. Do not use a default `quorum: 1` for a three-voter strict volume.
- Implement bounded graceful shutdown: stop admission, drain or fail pending requests correctly, persist required state, close listeners, and unmount/close resources.
- Keep health endpoints separate from the data protocol and derive health from live state, not static config values.

**Exit criteria**
- Three independently launched Linux daemon processes with separate data directories discover/elect a leader and form exactly one healthy three-voter cluster.
- A process restart recovers the same node identity and committed state from disk.
- Readiness reports not-ready until recovery and cluster state satisfy the documented policy.
- Liveness reports process health independently of quorum; quorum loss is not falsely reported as a healthy write service.
- SIGTERM completes within a documented timeout and leaves recoverable state.
- The daemon exposes real QUIC peer/client endpoints; metrics HTTP is not mistaken for the storage protocol.

### T03 — Wire the complete durable write and metadata path

**Priority:** P0 · **Dependency:** T02; coordinator, chunkstore, metadata, and Raft foundations.

**Work**
- Wire every FUSE mutation through the volume coordinator.
- Preserve the required ordering: prepare and durably store referenced chunks on a majority, then commit the metadata command through Raft, then apply/acknowledge according to the spec.
- Use stable operation IDs for retry deduplication and expose operation lookup for ambiguous outcomes.
- Persist create, write, append, truncate, chmod/chown, link, symlink, unlink, mkdir/rmdir, rename, xattr and directory metadata through the same authoritative path.
- Make `fsync` wait for the documented quorum durability condition. Make `fsyncdir` wait for namespace-command durability. Do not treat FUSE `Flush` or `Release` as substitutes for `Fsync`.
- Remove production reachability of `NoopSyncer`. Keep it test-only and make tests explicitly label it as non-durable.
- Make truncate and other metadata-only operations use the coordinator; they must not mutate local state and then return success without replication.

**Exit criteria**
- After a successful write + `fsync`, kill the leader and restart the cluster: the acknowledged bytes remain intact.
- After a successful write + `fsync`, permanently lose any one voter: the two-node majority can read the acknowledged bytes and continue accepting writes.
- With no majority, all mutations and durability calls fail with documented errors; no uncommitted state is exposed as committed.
- Retry the same operation ID after a lost response: it is applied exactly once and its final result is discoverable.
- `fsync`/`fsyncdir` cannot return success before their required durability and commit conditions.
- Tests cover create/write/truncate/rename/unlink crash points and verify old-or-new atomic state, never mixed state.

### T04 — Complete the kernel-visible FUSE inode tree and POSIX contract

**Priority:** P0 · **Dependency:** T03.

**Work**
- Replace the flat root-only Handler tree with the persistent metadata/inode model.
- Implement lookup, readdir, forget/inode lifecycle, getattr/setattr, open/read/write, create, mkdir/rmdir, link/symlink/readlink, unlink, rename, and directory traversal for arbitrary depth.
- Ensure directory children are represented by persistent inode IDs, not root-level names or process-local pointers.
- Match Linux rename semantics between the Handler and metadata state machine. Resolve the current mismatch where the Handler permits replacement but the state machine refuses an existing destination.
- Implement `O_CREAT`, `O_EXCL`, append semantics, permission checks, hard-link counts, symlink traversal, and advisory lock forwarding through actual kernel requests.
- Make all limits consistent. Current documentation describes 16 MiB Handler, 32 MiB state-machine, and a 16 GiB example volume max-file limit. Use configured `VolumeSpec` limits and return one consistent error.
- Return explicit documented errors for excluded operations such as writable shared `mmap` and `O_DIRECT`; do not leave accidental `ENOSYS` behavior where the contract promises an explicit unsupported errno.

**Exit criteria**
- A Linux integration test mounts SyncFS and successfully creates `a/b/c/file`, writes/reads it, renames it across directories, links it, unlinks one link, restarts a client, and verifies the same namespace/data.
- A second mount observes committed changes without stale reads after lease revocation/expiry.
- Real kernel calls exercise `O_CREAT|O_EXCL`, append, truncate, `fsync`, `fsyncdir`, `flock`, and POSIX byte-range locks.
- Permission tests cover owner/group/other and root behavior through the kernel adapter, not only direct Handler calls.
- The POSIX matrix is generated or test-backed and every claimed DONE operation has a kernel-level test.

### T05 — Close authentication and authorization gaps

**Priority:** P0 · **Dependency:** T02.

**Work**
- Replace enrollment Get-then-Mark with an atomic consume operation (transaction/CAS/unique state transition) so concurrent uses cannot both succeed.
- Enforce the stream opener's principal role inside `OpenClassifiedStream` or a single mandatory authenticated transport wrapper. Do not rely on each caller remembering to invoke `ValidatePrincipalForClass`.
- Enforce role, cluster ID, node/client/admin identity, URI SAN cardinality, expiry, and certificate chain on both sides of every connection/stream class.
- Keep TLS 1.3, ALPN `syncfs/1`, and 0-RTT disabled; test downgrade and resumption paths.
- Implement certificate rotation/revocation/expiry behavior and ensure private keys/enrollment material never appear in logs or diagnostics.
- Add immutable audit records for bootstrap, enrollment, membership, certificate, volume-policy and forced-recovery changes.

**Exit criteria**
- A concurrent test racing N uses of one enrollment bundle produces exactly one successful consumption.
- Unauthorized stream openers and acceptors are rejected before application payload is processed.
- Wrong cluster, wrong role, missing/multiple URI SAN, expired/revoked certificate, TLS downgrade, and 0-RTT cases are rejected.
- Certificate rotation is exercised without losing cluster quorum; expired credentials are rejected.
- Secret-scrubbing tests prove private keys, tokens and enrollment bundles are not logged.
- Audit events are durable, structured, attributable and cannot be silently disabled by ordinary node configuration.

### T06 — Finish metadata persistence, snapshots, compaction, and GC safety

**Priority:** P0 · **Dependency:** T03.

**Work**
- Implement the persisted metadata projection or document/implement deterministic full replay from Raft as the sole supported recovery mechanism. Do not leave an ambiguous in-memory scaffold.
- Implement durable snapshot creation, including metadata state, applied/committed index, cluster/volume/format identity, referenced chunk manifest, checksums, and required membership state.
- Implement atomic snapshot install/restore and safe WAL compaction only after the snapshot and its containing directory entry are durable.
- Integrate resumable snapshot/chunk transfer with learner catch-up.
- Integrate GC reachability with live metadata, retained logs, active snapshots, in-flight writes, pending operations, and learner transfer pins. A chunk must not be deleted while any valid recovery path can reference it.
- Implement scrub/repair and distinguish corruption detection, quarantine, and successful majority repair.

**Exit criteria**
- Restart from a verified snapshot plus WAL tail reconstructs exactly the same applied metadata index and chunk references.
- Crash injection at each snapshot write/fsync/rename/compaction boundary recovers to a valid old or new snapshot, never a partial accepted snapshot.
- GC tests prove every chunk referenced by live metadata, retained logs, snapshots, in-flight writes or learner transfers survives.
- A learner can resume an interrupted snapshot transfer, verify all hashes, catch up, and only then become eligible for promotion.
- Disk-full, truncated WAL, corrupt snapshot, missing chunk and corrupted pack tests fail closed and provide a documented recovery action.

### T07 — Complete real membership and replacement-node lifecycle

**Priority:** P0 · **Dependency:** T06; Raft driver and enrollment.

**Work**
- Wire membership changes to the actual Raft ConfState/joint-consensus mechanism; do not leave ConfState correctness as an operator convention.
- Implement add learner, verify snapshot, catch up, promote, drain, remove, and replace-failed-voter workflows as real authenticated control-plane operations.
- Make learner state non-voting and non-serving until all promotion prerequisites are committed.
- Enforce one in-flight membership change and validate quorum before, during and after joint consensus.
- Persist and recover membership transitions across node restarts.

**Exit criteria**
- An added learner cannot vote, lead, or serve application I/O before verified catch-up and committed promotion.
- Replacing a failed voter never reduces the surviving cluster below a valid quorum.
- Promotion/removal succeeds through the real control plane and Raft ConfState after process restart.
- Tests cover leader failure during joint consensus, learner disconnect during transfer, duplicate admin requests and stale membership commands.
- A documented operator procedure replaces one failed voter without rebuilding the whole cluster or manually editing internal state.

### T08 — Turn the fault harness into real process/network/storage tests

**Priority:** P0 · **Dependency:** T02–T07.

**Work**
- Run three actual SyncFS daemon processes with independent persistent data directories and real QUIC/TLS sockets. Keep the in-memory mesh as a fast unit-test layer, not the only HA evidence.
- Add deterministic process kill/restart, SIGKILL at persistence hooks, packet delay/loss/duplication, network partition, disk-full, read-only filesystem, I/O error, corrupt pack/WAL, clock skew and client restart scenarios.
- Use `tc netem` or an equivalent reproducible network fault mechanism. Record seed, fault schedule, kernel, filesystem, storage device and commit SHA.
- Assert invariants against the mounted filesystem and operation results, not internal fields alone.

**Exit criteria**
- All technical-spec failure scenarios pass against real processes: leader loss, follower loss, minority partition, interrupted chunk upload, restart at persistence boundaries, disk-full, corruption, client reconnect and learner promotion.
- No acknowledged mutation is lost after any single-node failure.
- No minority node acknowledges writes, grants locks or serves prohibited strict reads.
- Repeated deterministic seeds produce reproducible results and preserve event histories proving safety.
- The integration suite runs in a reproducible Linux CI environment and is required before merge/release.

### T09 — Implement production observability and operator runbooks

**Priority:** P1 · **Dependency:** T02, T06, T07.

**Work**
- Wire the collector to live Raft, coordinator, storage, lease, lock, GC, repair and certificate state. Remove static `LeaderQuorum`/index values and constant health responses.
- Expose separate liveness and readiness endpoints. Readiness must describe the node's actual ability to serve the documented operation class; quorum loss must not be reported as healthy write readiness.
- Record metrics for leader/quorum, committed/applied index, follower lag, learner catch-up, lease-revoke latency, lock waits, ambiguous operations, chunk checksum failures, repair/GC backlog, free disk, certificate expiry and snapshot checkpoint.
- Add structured logs with node/cluster/volume/request/operation IDs and secret redaction.
- Write operator runbooks for no quorum, failed leader, node replacement, disk exhaustion, corrupted chunk/WAL, client disconnect, cert rotation, snapshot restore, upgrade and emergency read-only mode.
- Define alert thresholds and recovery objectives; do not invent thresholds without qualification data.

**Exit criteria**
- Metrics change when real cluster state changes; tests demonstrate leader loss, quorum loss, follower lag, low disk, repair backlog and cert expiry.
- Health checks distinguish process liveness from cluster readiness and cannot return healthy based only on a configured quorum number.
- Every high-severity fault test has an observable signal and an operator runbook.
- Recovery procedures are executed in a timed restore/replacement drill and results are recorded.

### T10 — Harden systemd deployment and configuration

**Priority:** P1 · **Dependency:** T02, T05, T09.

**Work**
- Define a dedicated service account/group and exclusive state directory ownership for `/var/lib/syncfs`.
- Add tested systemd sandboxing and capability/device restrictions. The current daemon unit only contains comments mentioning hardening; comments do not enforce `ProtectSystem`, `ProtectHome`, etc.
- Separate storage-node daemon and application-host mount service configuration. Make the mount unit's ordering/requirements explicit so the application cannot start against an absent or unhealthy mount.
- Use explicit config files for node identity, data path, peer endpoints, certificates and volume/mount selection. Validate permissions and reject unknown or invalid fields.
- Add mount health/ownership checks, safe unmount behavior, restart policy and resource limits. Avoid running the storage daemon as root unless a documented capability requirement makes it necessary.

**Exit criteria**
- `systemd-analyze verify` succeeds for all units; `systemd-analyze security` findings are reviewed and exceptions documented.
- Daemon runs with the minimum required privileges and can write only its owned data/config/runtime paths.
- Mount starts before its application and the application is stopped or held back when the mount is unavailable according to the chosen service policy.
- A clean install, reboot, daemon restart, mount restart and clean uninstall succeed on every supported Linux distribution.

### T11 — Make CI enforce the real quality gates

**Priority:** P1 · **Dependency:** implementation tasks above.

**Work**
- Keep `go mod verify`, `go vet`, `go test ./...`, `go test -race ./...`, and add an explicit `go build ./...` gate.
- Run `buf lint`, `buf breaking --against api/baseline`, and generated-code consistency checks in CI.
- Make formatting fail the job when `gofmt -l -s .` returns files; do not rely on a formatter command that edits the tree and always succeeds.
- Replace the conditional “skip if directory absent” FUSE gate with a required Linux FUSE integration job on an environment where `/dev/fuse` is available. A missing FUSE device must fail the production integration job, not silently remove the test.
- Run the real three-process fault suite on a scheduled and pre-release runner; run a suitably scoped version on pull requests.
- Add dependency vulnerability scanning, license/SBOM checks and build reproducibility checks.
- Pin release tooling to a reviewed version instead of `goreleaser latest`; sign release artifacts and attest build provenance.

**Exit criteria**
- All listed checks are required branch-protection checks for merge/release.
- CI covers Linux/amd64 and Linux/arm64 for build/unit tests; actual supported kernel/filesystem combinations are covered by qualification runners.
- A deliberately broken protobuf change, formatting error, race, failed FUSE mount test, and failed fault test each cause CI failure.
- Release artifacts include checksums, SBOM, signature and provenance tied to the source commit.

### T12 — Complete reference-hardware and target-application qualification

**Priority:** P0 for production support claim · **Dependency:** T03–T11.

**Work**
- Run on the reference profile in ADR-0001: three independent storage nodes, NVMe devices that honor flushes, ext4/XFS with documented options, and low-latency 10 GbE.
- Measure the complete path, including FUSE, client, QUIC, leases, Raft, chunk writes, metadata apply and actual fsync. Do not use the in-process benchmark as a substitute.
- Capture p50/p95/p99/max latency, throughput, CPU, memory, disk utilization, network throughput, queue depth and variance under steady state and one-node failure.
- Run the specified gates: 4 KiB quorum writes at 1,000 ops/s with p99 ≤ 25 ms; create/write/rename at 500 ops/s with p99 ≤ 30 ms; strict cache-hit reads p99 ≤ 2 ms. If these targets are not met, amend them through an ADR backed by measured workload evidence before changing the support promise.
- Choose one specific target application/version and run its vendor-supported shared-home or clustered-storage suite on two application hosts over separate SyncFS mounts.
- Exercise application failover, restart, node replacement and recovery while validating the application's own consistency checks.

**Exit criteria**
- Every §11 benchmark has raw output, hardware/kernel/filesystem/config details, commit SHA, exact commands and reproducible analysis.
- All accepted performance targets pass under steady state and the specified degraded mode, or are formally revised with evidence before release.
- The chosen application passes its vendor suite plus SyncFS fault scenarios; failures are fixed or explicitly excluded from the support statement.
- README names only the exact tested application/version and platform combinations. No generic Jira/Confluence/repository-manager compatibility claim without evidence.

### T13 — Release, upgrade, rollback, and recovery qualification

**Priority:** P1 · **Dependency:** T09–T12.

**Work**
- Define protocol, WAL, snapshot and metadata format compatibility rules and supported upgrade paths.
- Test rolling upgrades one voter at a time while retaining quorum, including mixed-version protocol negotiation and restart after each upgraded node.
- Define downgrade/rollback limits. Never downgrade a persisted format unless an explicit, tested reverse migration exists.
- Provide versioned binary packages or a deterministic install procedure, checksums, signatures, SBOM, provenance and a release qualification report.
- Run backup/snapshot restore drills into a clean cluster and document RPO/RTO based on actual measured recovery.

**Exit criteria**
- A supported N-1 to N upgrade completes without losing quorum or acknowledged data.
- A failed upgrade has a documented safe recovery path; unsupported rollback is explicitly prevented rather than silently attempted.
- A clean-cluster restore succeeds from the documented backup/snapshot artifacts and passes data integrity verification.
- Every release is traceable to source commit, toolchain, dependencies, tests, benchmark report and signed artifacts.

## 4. Release gate — Definition of Production Ready

Do not call the release production-ready until all of the following are true:

- [ ] T00–T08 complete; no P0 correctness, security or recovery work remains.
- [ ] A real three-node cluster runs on independent Linux hosts with persistent ext4/XFS storage.
- [ ] Real FUSE mounts support the published POSIX profile, including nested directories.
- [ ] Every successful write/namespace operation and every successful `fsync`/`fsyncdir` satisfies the quorum durability contract.
- [ ] Losing one voter does not lose acknowledged data; a minority cannot acknowledge mutations.
- [ ] Restart, disk-full, corruption, snapshot restore, learner join and voter replacement have reproducible evidence.
- [ ] Authentication and authorization are enforced centrally and fail closed.
- [ ] T09–T11 complete: actual health/metrics, runbooks, hardened deployment and required CI gates.
- [ ] T12 passes on reference hardware and for one named target application/version.
- [ ] T13 passes: signed release, upgrade, restore and rollback policy.
- [ ] README, support matrix, known limitations and status document match the tested product exactly.

## 5. Recommended execution order

1. T00–T01: freeze scope and make the engineering tracker truthful.
2. T02: build the real daemon runtime and control-plane composition.
3. T03 and T06: finish durable writes, metadata persistence, snapshots and compaction.
4. T04: complete the kernel-visible FUSE/POSIX layer against the durable coordinator.
5. T05 and T07: close authentication and membership lifecycle gaps.
6. T08: run real process/network/storage fault tests.
7. T09–T11: observability, operational hardening and mandatory CI.
8. T12–T13: reference-hardware, target-application and release qualification.

**Do not parallelize T03/T04/T06 across incompatible interfaces.** Agree on the coordinator, inode, operation-ID, durability, and snapshot contracts first. Parallel work is appropriate for independently testable security, runbooks, CI, and packaging only after the interfaces are stable.

## 6. Review limitations

This assessment used the public GitHub README, rendered files, and raw source files on `main`. A local clone and independent execution of the Go test suite were not possible in the review environment because outbound DNS/network access to GitHub was unavailable. Test results and benchmark values above are therefore identified as repository-reported evidence, not independently reproduced results. The code-level blockers cited above were inspected directly in the published source.
