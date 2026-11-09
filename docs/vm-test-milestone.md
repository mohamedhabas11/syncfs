# VM test milestone (v0.1-test)

**Goal:** Deploy three `syncfs daemon` processes on test VMs/containers,
verify cluster formation, health, restart recovery, and Raft proposal flow —
**not** production readiness.

**Status (2026-11-09, `9c04570` → Iter 7):** READY FOR VM FLEET TEST (daemon +
FUSE demo).
Full `go test ./...` green in Docker; `bootstrap` generates fleet; daemons
serve live `/healthz`/`/readyz`/`/metrics` + `POST /raft/propose` +
`POST /volume/chunks` + `POST /volume/command` (quorum chunks + Raft commit);
restart replays WAL + metadata projection; `syncfs mount --daemon <url>`
wires FUSE durability through `WireSyncer` + `syncfs admin test-file` proves
chunk quorum + metadata commit end-to-end (in-memory Handler tree is the
test surface; full metadata inode-ID binding is T04 remainder).
Election via QUIC is transport-proven (persistent conns deliver Raft
messages) but deterministic 3-process leader election needs VM-clock
verification — see `docs/deploy-vm.md` step 3 (sequential start).

## Ready when (checklist for VM run)

- [x] `go build ./...`, `go vet ./...`, `go test ./... -race` green (Docker `golang:1.26`)
- [x] `scripts/bootstrap-fleet.sh` generates `./fleet` (CA + 3 configs)
- [ ] Three daemons start with `syncfs daemon --config ./fleet/node-{1,2,3}.json` (sequentially, 250ms+ apart)
- [ ] `/healthz` 200 on all, `/readyz` 200 on at least one after election
- [ ] `POST /raft/propose` on the leader returns 202 for a small payload; payload replays after daemon restart (WAL + metadata projection recovered)
- [x] `syncfs admin status --endpoint <leader>` prints health/ready
- [ ] `syncfs admin test-file --endpoint <leader> --name hello --data world` writes durably (chunk quorum + metadata commit)
- [ ] `syncfs mount --volume default --target /mnt/syncfs --daemon http://<leader>` mounts; `echo hi > /mnt/syncfs/f` + `fsync` succeeds via daemon; second mount reads after restart (local-tree demo; metadata binding pending)
- [ ] `docs/deploy-vm.md` steps reproduce on fresh VMs (record evidence per run)

## Explicitly not ready

- FUSE shared-directory writes (mount remains fail-closed `EIO` without coordinator)
- §11 perf gates (UNVERIFIED without 3× NVMe + 10GbE)
- Target-app qualification, upgrades/rollback, signing/SBOM

**Evidence to record:** commit SHA, `go test` output, `bootstrap` output, `curl /healthz`/`/readyz` outputs, restart log, target distro/kernel/storage/network.
