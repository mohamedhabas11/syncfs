# VM test fleet (v0.1-test milestone)

Deployable test milestone (not production): three `syncfs daemon`
processes form one Raft cluster, expose live health/readiness/metrics,
recover WAL + metadata on restart, and shut down bound by
`shutdown_timeout`. FUSE mount is fail-closed without a coordinator (honest);
use this fleet to test election, restart recovery, and observability on VMs.

## 1. Build

```sh
docker build -t syncfs:test .
# or locally:
go build -o syncfs ./cmd/syncfs
```

## 2. Bootstrap (offline)

```sh
./scripts/bootstrap-fleet.sh cl-test ./fleet
# creates ./fleet/{ca.pem,node-{1,2,3}.json,node-{1,2,3}.pem,node-{1,2,3}.key,data-{1,2,3}/}
```

## 3. Run (three hosts or one host with distinct ports)

```sh
syncfs daemon --config ./fleet/node-1.json &
syncfs daemon --config ./fleet/node-2.json &
syncfs daemon --config ./fleet/node-3.json &
```

Health checks (liveness vs readiness):

```sh
curl -s http://127.0.0.1:18081/healthz  # 200 while process serves
curl -s http://127.0.0.1:18081/readyz   # 200 only with leader + no chunk gap
./syncfs admin status --endpoint 127.0.0.1:18081
```

## 4. Verify restart recovery

```sh
pkill -TERM syncfs; sleep 2
syncfs daemon --config ./fleet/node-1.json &
curl -s http://127.0.0.1:18081/healthz  # 200
```

## 5. systemd (Linux hosts)

Units `deploy/systemd/syncfs-daemon.service` + `syncfs-mount.service` are
hardened (`ProtectSystem=strict`, `NoNewPrivileges`, `ReadWritePaths`, …).
Pre-create the service user and dirs:

```sh
sudo useradd --system --home /var/lib/syncfs --shell /usr/sbin/nologin syncfs || true
sudo install -d -o syncfs -g syncfs /var/lib/syncfs /etc/syncfs
sudo cp ./fleet/* /etc/syncfs/
sudo cp deploy/systemd/syncfs-daemon.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl start syncfs-daemon
sudo systemctl status syncfs-daemon
```

`systemd-analyze verify` / `security` need a Linux host — run there before
claiming hardening DONE.

## 6. Known limits

- Elections elect via Raft (in-memory `testkit/cluster` is the fast unit
  proof; a deterministic 3-process QUIC election test is still pending).
- No FUSE coordinator yet: mount writes are fail-closed (`EIO`), not silent.
- No §11 perf qualification (needs 3× NVMe + 10GbE, ADR-0001).
