# netem — network emulation profiles (separate table)

Docker Desktop Linux VM. **Not run in this environment** — kept separate from
`floor.md` / `sweep.md` per policy (netem numbers must never be mixed with
the no-netem baseline). `tc netem` requires `NET_ADMIN` inside the VM and a
second network namespace; not attempted via `docker compose run` here.

| Profile | RTT added | Clients | Volume | ops/s | p50 ms | p99 ms | floor-fraction | Status |
|---|---|---|---|---|---|---|---|---|
| baseline (no netem) | 0 ms | — | — | — | — | — | — | see `floor.md` / `sweep.md` |
| 0.5 ms | +0.5 ms | — | — | — | — | — | — | UNVERIFIED — not run |
| 2 ms | +2 ms | — | — | — | — | — | — | UNVERIFIED — not run |
| 20 ms | +20 ms | — | — | — | — | — | — | UNVERIFIED — not run |

Environment (when run): Docker Desktop Linux VM, tmpfs or named volume only,
commit sha + exact command per row. Absolute numbers never compared to
spec §11 (UNVERIFIED).
