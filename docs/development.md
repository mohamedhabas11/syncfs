# Development

SyncFS is a Linux-only project (FUSE). Develop on Linux. macOS may be used
for spec/docs work only — FUSE tests cannot run on macOS (see
`.spec/technical_spec.md` §13).

## Toolchain (M1-01 baseline)

- **Go 1.26.0** (REQUIRED; `go 1.26.0` in `go.mod`). quic-go ≥ v0.62
  requires Go 1.26 (see `.spec/technical_spec.md` §6).
- Setup Go 1.26 on Linux: use your distro packager or tarball from
  `https://go.dev/dl/`. Verify with `go version` → `go1.26.0`.
- Bumping the Go floor: change `go.mod` + CI `go-version` together, run
  `go mod tidy`, note in commit message. Never depend on a moving branch.

## Pinned dependencies (M1-01, reviewed 2026-11-08)

| Module | Version | License | Notes |
| --- | --- | --- | --- |
| `github.com/hanwen/go-fuse/v2` | v2.12.1 | BSD-3 | FUSE adapter; walk-past `v2.12.1` needs tag audit |
| `go.etcd.io/etcd/raft/v3` | v3.5.35 | Apache-2.0 | Stable (avoid `v3.6.0-alpha.0`) |
| `github.com/quic-go/quic-go` | v0.65.0 | MIT | ≥ v0.62 required for `SetPriority`; `Allow0RTT=false` default |
| `github.com/cockroachdb/pebble` | v1.1.5 | BSD-3 | LSM; `WriteOptions.Sync` policy required (M2-02) |
| `lukechampine.com/blake3` | v1.4.1 | BSD-3 | content addressing (M2-02; see DECISIONS.md) |
| `google.golang.org/protobuf` | v1.36.12 | BSD-3 | Stable (avoid `v1.37.0-rc.1`) |

- Exact pins live in `go.mod`/`go.sum`. `go.sum` is committed.
- License/SBOM policy: MIT/BSD/Apache-2.0 packages are acceptable. New
  dependencies MUST have their LICENSE file reviewed before `go get`.
  Generate the SBOM with `go list -m all` until M5-01 adds a pinned SBOM
  tool. Every bump is a separate commit with rationale.

## Commands

```text
make mod-verify   # go mod verify
make test         # go test ./...
make vet          # go vet ./...
make lint         # vet + gofmt + buf lint
make build        # go build ./...
make fleet-test   # container fleet: docker compose run --rm node-a go test ./... -race
```

## Development containers (3-node fleet)

The control/data-plane test fleet is `docker-compose.yml` — three `golang:1.26`
services (`node-a`, `node-b`, `node-c`). Code is shared read-only at `/src`;
**data is not shared**: each node owns a private writable volume
`/var/lib/syncfs` (named volumes `node-{a,b,c}-data`). Independent failure
domains are the whole point — quorum, corruption, and repair tests are
meaningless on shared mounts. This is the primary dev loop; host-side
`make test` is secondary.

```text
docker compose run --rm node-a go test ./... -count=1 -race   # one node
make fleet-test                                              # same via Makefile
docker compose up -d && docker compose exec node-b go version  # fleet up
```

Later milestones (control-plane enrollment, data-plane chunk transfer,
FUSE mount) run the daemon inside these same three containers. `/dev/fuse`
is exposed to every node (devices + SYS_ADMIN) and `fusermount` is installed
by the fleet Dockerfile. See `.spec/tasks.md` M1-03.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on `ubuntu-24.04` with
Go 1.26: `go mod verify`, `go vet`, `go test`, `go test -race`, and a
FUSE gate that runs once `internal/fusefs/` exists (M3-01).
`git diff --check` runs on every push.

## Verification evidence

- M1-01 verification: `go version`, `go mod verify`, `go test ./...`,
  `go vet ./...` — all with Go 1.26.0 on Linux CI. Local macOS Go
  (1.15.6 in this checkout's history) is NOT verification evidence.
- FUSE and network-fault tests (M3+) run on the container fleet
  (`TestFUSE`, `TestFault`) — no Linux host required.
