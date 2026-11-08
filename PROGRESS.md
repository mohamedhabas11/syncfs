# SyncFS Progress

Autonomous build log. One line per milestone: id, commit, commands, result.

| Date | Milestone | Commit | Commands run | Result |
| --- | --- | --- | --- | --- |
| 2026-11-08 | M1-01 Go baseline | 788f7af | go version / mod verify / test / vet | PASS (go1.26.0, no packages yet) |
| 2026-11-08 | M1-02 api/v1 schemas | 9c35f54 | buf lint / breaking / generate, go test ./api/... | PASS |
| 2026-11-08 | M1-03 auth/PKI | 2a7804c | go test ./internal/auth/... -race (fleet) | PASS |
| 2026-11-08 | M1-04 QUIC transport | 720ff6d | go test ./internal/transport/... -race (fleet) | PASS |
| 2026-11-08 | M2-01 WAL + snapshot store | 2645e09 | go test ./internal/raftstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-02 chunkstore packs+BLAKE3+Pebble | 7a261f7 | go test ./internal/chunkstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-03 metadata state machine | 1403df6 | go test ./internal/metadata/... -race (fleet) | PASS |
| 2026-11-08 | M2-04 raft driver + 3-node mesh | c89af8c | go test ./internal/raftstore/... -race (fleet) | PASS |
| 2026-11-08 | M2-05 quorum write coordinator | 9ae7f7b | go test ./internal/volume/... -race (fleet) | PASS |
| 2026-11-08 | M2-06 leases + locks + cache | c8fc0eb | go test ./... -count=1 -race (fleet) | PASS |
| 2026-11-08 | M3-01 FUSE mount (skeleton, honest partial → completed) | e133cab → eba1e4e | go test ./internal/fusefs/... -race (fleet) | PASS (completed) |
| 2026-11-08 | M3-02 POSIX compat surface | 82c4761 | go test ./internal/fusefs/... -race (fleet) | PASS |
| 2026-11-08 | M3-03 client failure semantics | 175e4e3 | go test ./internal/client/... -race (fleet) | PASS |
| 2026-11-08 | M4-01 snapshots+GC | 06d7450 | go test ./internal/chunkstore/gc/... -race (fleet) | PASS |
| 2026-11-08 | M4-02 membership replacement | 0c488d5 | go test ./internal/admin/member/... -race (fleet) | PASS |
| 2026-11-08 | M4-03 CLI + systemd | d5371f3 | go test ./cmd/... -race (fleet) | PASS |
| 2026-11-08 | M4-04 fault harness | 66bd4db | go test ./internal/testkit/... -race (fleet) | PASS |
| 2026-11-08 | Docs + fleet + diagrams | e133cab | mermaid diagrams; TestFUSEmountSmoke green | PASS |
| 2026-11-08 | Release prep | 2455728 | mkdocs build --strict; go build/vet/test-race | PASS |
| 2026-11-08 | M5 qualification | — | — | DEFERRED (ADR-0001) |
| 2026-11-08 | Docs reality pass (match code, trim fluff) | TBD | mkdocs build --strict; go test ./... -race | PASS |

## Spec-sheet progress tracking

| Spec sheet | Status |
| --- | --- |
| diagrams/docs | architecture.md write-path + read/lease flows; operating-model bootstrap joining (mermaid); mkdocs site with architecture/ sub-pages |
| PROGRESS.md | whole-build + this file |
| spec reconciliation | D-001/Camp A adopted; Camp B archived |
| .spec/tasks.md | all M1–M4 ticked; M5 open per ADR-0001 |
| known-limitations + posix-compat | maintained honestly |

## M3-01 status (2026-11-08)

Delivered: mount skeleton (go-fuse v2), strict posture, kernel-mount smoke
test in the fleet, Lookup/Open/Getattr + errno table + handler surface.
In-memory state; coordinator persistence wiring follows with M5.

## Final (autonomous mode)

M2-02..M2-06 are complete: raft WAL/snapshot store, BLAKE3 chunkstore, metadata
state machine, raft driver with 3-node mesh, quorum write coordinator, read
leases + lock state + client cache. Clean-tree gate passed: `go build ./...`,
`go vet ./...`, and `go test -race ./...` all green (12 packages). Tracked
the blake3 dependency in a final tidying commit (dd209fa). Two independent
bugs found+fixed by tests this session: (1) lock-range disjoint logic was
inverted; (2) 3-node election deadlocked on lockstep ticking with PreVote
(each node self-voted first) — fixed by single-node ticking per round in the
mesh harness (per etcd follower-election design).
