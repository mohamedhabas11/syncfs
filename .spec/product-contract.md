# Product contract (T00 lock)

**Status:** Locked baseline for evaluator T00 (2026-11-09). Authoritative over historical research and watcher documents. Linked from `README.md`.

## 1. Product

SyncFS is a Linux-only, FUSE-mounted, three-voter, strongly consistent shared-directory volume. Small high-value shared directories (shared home for repository managers / artifact servers / Jira-class HA services). Not a capacity store, not a WAN system, not a watcher replicator.

## 2. Deployment profile (first supported)

| Concern | Locked value |
| --- | --- |
| Distro/kernel | Ubuntu 24.04 LTS, Linux 6.8+ x86_64/aarch64 (kernel must expose `/dev/fuse` + `fusectl`); other distros unsupported until `systemd-analyze verify` + FUSE smoke pass in CI qualification |
| Backend FS | ext4 `data=ordered,barriers=on,commit=5s` **or** XFS with `barrier`+`fua` honoring flushes, on NVMe honoring flushes; LUKS/dm-crypt optionally below `/var/lib/syncfs` |
| Network | 10 GbE low-latency LAN between voters; UDP peer port reachable voter↔voter; no internet dependency |
| Topology | Exactly 3 storage voters (independent power/failure domains) + 0–1 learner during replacement; N application hosts each with one `syncfs mount` (mount before app unit) |
| Volume limits | `VolumeSpec`: `ChunkSizeBytes=65536` (immutable), `QuotaBytes` + `MaxFileBytes` enforced at chunk-admission before quorum receipts, volume-quota only (per-dir/user is post-MVP). Interim Handler caps (16 MiB) and state-machine caps (32 MiB) are **not** the contract — must be unified on `VolumeSpec` by T04 (see `docs/posix-compat.md`) |
| Target app | **No supported app yet.** First qualification target is TBD (M5-02 DEFERRED, ADR-0001). `docs/operating-model.md` example `jira-shared-home` is illustrative only and must not be read as a compatibility claim |
| Transport | mTLS 1.3 over QUIC, ALPN `syncfs/1`, resumption allowed, 0-RTT disabled (`transport.Allow0RTT=false`, enforced) |
| Durability | Quorum-durable: mutation acknowledged only after chunk majority + Raft majority commit (spec §8.2) |

## 3. Exclusions (fail-closed errnos, not silent)

| Excluded | User-visible behavior |
| --- | --- |
| WAN/multi-region, disconnected writes, conflict resolution | Unsupported deployment; `Quorum lost → EIO/ENOTCONN`, never async success |
| Two-node strong mode, direct brick mounts, NFS/SMB gateway | Unsupported topology; no code path exists |
| Writable shared `mmap` | Excluded (`EOPNOTSUPP` required; current kernel default `ENOSYS` pending explicit `Mmap` handler — see `docs/known-limitations.md` #2) |
| `O_DIRECT` | `EINVAL` in strict mode on Linux (build-tagged `oDirectFlag`); 0 off-Linux (tests skip) |
| Device files / sockets / FIFOs | `EINVAL` (handler-surface; FUSE adapter has no handler → `ENOSYS` until T04 wires explicit errnos) |
| At-rest app encryption / KMS | Out of scope; LUKS below daemon is the MVP boundary |

## 4. Operation contracts (summary; detail in `docs/posix-compat.md` + `docs/failure-semantics.md` + spec §8)

- **Success means durable:** `write`/`truncate`/`rename`/`create`/`mkdir`/`link`/`unlink`/`chmod`/`fsync`/`fsyncdir`/lock grant/admin mutation return success only when the spec §8.2 quorum + Raft-commit + apply condition is met. `Flush`/`Release` never imply durability.
- **Ambiguous outcomes:** `EIO`/`ENOTCONN` means *unknown* — client must `GetOperation(OperationID)` before retrying; never re-execute as a fresh operation after the 10k/10-min retention window.
- **Invalidation:** strict mounts use zero TTLs + `FOPEN_DIRECT_IO`; stale reads after lease revoke/expiry are forbidden (Inv.6/10).

## 5. Superseded documents

`arch/architecture-proposal.md`, `docs/directory-daemon.md`, and any watcher-based research describing filesystem events / pull sync without Raft ordering are **superseded** by (in order): (1) `.spec/technical_spec.md`, (2) this contract, (3) `docs/operating-model.md`, (4) `docs/posix-compat.md`. They are retained for history and must not be used as implementation contracts. The README links this file as authoritative; historical research under `research/` is design history, not spec.

## 6. Change control

Any deviation (new topology, new excluded-op errno, new perf gate, new backend) requires an ADR with measured evidence and an explicit support-contract change per task-sheet rule 6. Roadmap items that widen MVP scope without an ADR are out of contract.
