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
| 2026-11-08 | M2-06 leases + locks + cache | c8fc0eb | go test ./... -count=1 -race (fleet), go build/vet ./... (host go1.26.0) | PASS |
| 2026-11-08 | M3-02 POSIX compat surface | TBD | go test ./internal/fusefs/... -race (fleet) | PASS |
| 2026-11-08 | M3-01 FUSE skeleton (partial) | e133cab | mermaid diagrams; TestFUSEmountSmoke green in container | PARTIAL |

## Spec-sheet progress tracking

| Spec sheet | Status | Next |
| --- | --- | --- |
| diagrams/docs | architecture.md write-path + read/lease flows; operating-model bootstrap joining (mermaid) | compat matrix mermaid pending M3-02 |
| PROGRESS.md | whole-build + this file | one line per milestone continues |
| spec reconciliation | D-001/Camp A adopted; Camp B archived | — |
| .spec/tasks.md | M3-01 honest OPEN (skeleton only) | full skeleton then M3-02 |

## M3-01 honest status (2026-11-08)

Delivered: mount skeleton (go-fuse v2, strict `AllowOther=false`, FsName syncfs),
real kernel-mount smoke test in the fleet (`TestFUSEmountSmoke` PASS),
Dockerfile (golang:1.26-alpine + fusermount + gcc) + per-node /dev/fuse wiring.
NOT yet delivered: caller-credential extraction in Lookups, direct-I/O
mutable-handle policy, exact Linux errno mapping table, full FUSE lifecycle
(unmount flushing), oplogging of xattr/ACL namespaces. M3-01 checkbox remains
OPEN; the skeleton is a partial step. M3-02 (compatibility profile) is next.

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
