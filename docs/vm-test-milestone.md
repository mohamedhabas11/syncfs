# VM test milestone (v0.1-test)

**Goal:** Deploy three `syncfs daemon` processes on test VMs/containers,
verify cluster formation, health, restart recovery, and Raft proposal flow —
**not** production readiness.

## Ready when

- [ ] `go build ./...`, `go vet ./...`, `go test ./... -race` green (Docker `golang:1.26`)
- [ ] `scripts/bootstrap-fleet.sh` generates `./fleet` (CA + 3 configs)
- [ ] Three daemons start with `syncfs daemon --config ./fleet/node-{1,2,3}.json`
- [ ] `/healthz` 200 on all, `/readyz` 200 on at least one after election (or documented pending with in-memory mesh evidence)
- [ ] `POST /raft/propose` on the leader returns 202 for a small payload; payload replays after daemon restart (WAL + metadata projection recovered)
- [ ] `syncfs admin status --endpoint <leader>` prints health/ready
- [ ] `docs/deploy-vm.md` steps reproduce on fresh VMs

## Explicitly not ready

- FUSE shared-directory writes (mount remains fail-closed `EIO` without coordinator)
- §11 perf gates (UNVERIFIED without 3× NVMe + 10GbE)
- Target-app qualification, upgrades/rollback, signing/SBOM

**Evidence to record:** commit SHA, `go test` output, `bootstrap` output, `curl /healthz`/`/readyz` outputs, restart log, target distro/kernel/storage/network.
