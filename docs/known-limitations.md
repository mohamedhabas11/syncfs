# Known limitations

Honest register. Nothing on this list is silently dropped. Items 7–14 come
from the adversarial audit in `research/01-critic-findings.md` +
`research/02-tester-evidence.md` (2026-11-08); the checked-in suite is green
without the attacker harness, but the listed durability/consensus caveats
remain until fixed and re-tested.

| # | Limitation | Impact | Recovery / path |
| --- | --- | --- | --- |
| 1 | xattr / ACL namespaces | PARTIAL | user.* get/set live with EOPNOTSUPP elsewhere; ACL policy enforcement pending |
| 2 | writable shared mmap | `EOPNOTSUPP` | none (excluded by design) |
| 3 | O_DIRECT explicitness | `EINVAL` in strict mode | use streaming reads |
| 4 | offline writes | unavailable | designed out (strict volume) |
| 5 | target-app qualification | deferred to M5 (ADR-0001) | needs reference hardware + chosen target app |
| 6 | persisted metadata index beyond Raft | in-memory scaffold | coordinator lands with M5; FUSE syncer seam implemented but production coordinator wiring pending |
| 7 | WAL snapshot restore + compaction durability | restarted node loses ConfState; `Compact` not durable | manual: re-bootstrap node from verified snapshot; do not rely on `Compact` until fixed |
| 8 | WAL gap poisoning + middle corruption | rejected gap write bricks reopen; middle corruption silently truncates | manual: quarantine node, rebuild WAL from quorum snapshot |
| 9 | metadata inode-ID determinism | `crypto/rand` IDs diverge across replicas | manual: coordinated rebuild; fix is deterministic ID from `(OpID, parent, name)` (not yet applied) |
| 10 | metadata parent panics | missing-parent Link/Rename panics follower apply | manual: restore follower from snapshot; fix adds parent checks (not yet in metadata) |
| 11 | chunkstore middle corruption + dedup receipt | earlier-record corruption drops later durable chunks; dedup returns receipt for corrupt bytes | manual: scrub + repair from majority; do not trust receipts until verified |
| 12 | enrollment double-spend + role enforcement | concurrent consume can succeed twice; client cert may present on raft port | manual: rotate enrollment nonces; transport must enforce `VerifyConnection` role (not yet wired) |
| 13 | volume coordinator atomicity | failed batch leaves orphan chunk; no sentinel `ErrNoQuorum`; ignores cancelled ctx | manual: run conservative GC sweep to reclaim orphans |
| 14 | benchmark qualification gap | numbers are in-process only (no QUIC/FUSE/10 GbE) | run §11 gates on reference hardware — see `docs/benchmarks/README.md` |

Rule: nothing here is a compatibility or performance certification.
