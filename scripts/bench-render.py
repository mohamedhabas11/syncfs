#!/usr/bin/env python3
"""Render bench-floor / bench-sweep tables (Docker Desktop policy).

Usage:
  python3 scripts/bench-render.py floor docs/benchmarks/floor-volume.json docs/benchmarks/floor-tmpfs.json
  python3 scripts/bench-render.py sweep docs/benchmarks/sweep-volume.json docs/benchmarks/sweep-tmpfs.json docs/benchmarks/floor-volume.json docs/benchmarks/floor-tmpfs.json

Every table states Docker Desktop Linux VM, volume type (tmpfs or named
volume; never a macOS bind mount), commit sha, and exact command. Absolute
numbers are NEVER compared against spec §11 targets (UNVERIFIED, see
docs/spec/status.md). Fresh payloads only; dedup state recorded.
Throughput is reported as a fraction of the measured fsync floor.
"""
import json, sys, pathlib

def load(p):
    with open(p) as f:
        return json.load(f)

def fmt(x, nd=2):
    return f"{x:.{nd}f}"

def render_floor(volume_path, tmpfs_path):
    vol = load(volume_path)
    tmp = load(tmpfs_path)
    lines = []
    lines.append("# bench-floor — write+fsync floor + RTT")
    lines.append("")
    lines.append("Docker Desktop Linux VM (aarch64, golang:1.26-alpine). Data on tmpfs or named volume only; never a macOS bind mount (`/src` is read-only code).")
    lines.append(f"Commit: `{vol.get('commit_sha','')}` (both runs). Absolute numbers are NEVER compared against spec §11 targets (UNVERIFIED: needs 3x NVMe honoring flushes + 10 GbE, see docs/spec/status.md). Fresh payloads only; dedup: " + vol.get("dedup_note","") + ".")
    lines.append("Outer (repro) commands (Makefile): `docker compose run --rm -e COMMIT_SHA=<sha> bench go run ./cmd/bench-floor -dir /bench/volume/floor -volume-type named_volume -n 500 -peer node-b` (named volume) and `... -dir /bench/tmpfs/floor -volume-type tmpfs -n 500 -peer node-b` (tmpfs). Per-row `Command` is the in-container argv.")
    lines.append("")
    lines.append("| Volume | Command | ops/s | p50 ms | p99 ms | max ms | alloc B/op | mallocs/op | CPU ms/op | fsyncs/entry |")
    lines.append("|---|---|---|---|---|---|---|---|---|---|")
    for r in (vol, tmp):
        cmd = f"`{r['command']}`"
        lines.append(f"| {r['volume_type']} (`{r['data_dir']}`) | {cmd} | {fmt(r['ops_per_sec'],1)} | {fmt(r['p50_ms'])} | {fmt(r['p99_ms'])} | {fmt(r['max_ms'])} | {fmt(r['alloc_bytes_per_op'],0)} | {fmt(r['mallocs_per_op'],1)} | {fmt(r['cpu_ms_per_op'],3)} | {r['fsyncs_per_committed_entry']} |")
    lines.append("")
    lines.append("## RTT (TCP connect, 20 attempts)")
    lines.append("")
    lines.append("| Path | Peer | min ms | p50 ms | p99 ms | Status |")
    lines.append("|---|---|---|---|---|---|")
    for r in (vol, tmp):
        lb = r.get("loopback_rtt_ms") or {}
        pr = r.get("peer_rtt_ms") or {}
        lines.append(f"| {r['volume_type']} loopback (127.0.0.1) | self | {fmt(lb.get('min',0))} | {fmt(lb.get('p50',0))} | {fmt(lb.get('p99',0))} | measured |")
        st = (r.get("peer_status") or "")[:60]
        lines.append(f"| {r['volume_type']} between-nodes (node-b:22) | node-b | {fmt(pr.get('min',0))} | {fmt(pr.get('p50',0))} | {fmt(pr.get('p99',0))} | {st} |")
    lines.append("")
    lines.append("Floor definition: raw `WriteAt` + `f.Sync()` per iteration (1 fsync/entry, dir sync amortized). Sweep throughput below is a fraction of this floor.")
    pathlib.Path("docs/benchmarks/floor.md").write_text("\n".join(lines) + "\n")
    print("wrote docs/benchmarks/floor.md")

def render_sweep(vol_path, tmp_path, fvol_path, ftmp_path):
    vol = load(vol_path)
    tmp = load(tmp_path)
    fvol = load(fvol_path)
    ftmp = load(ftmp_path)
    floors = {"named_volume": fvol["ops_per_sec"], "tmpfs": ftmp["ops_per_sec"]}
    lines = []
    lines.append("# bench-sweep — quorum writes vs client count (fresh 4 KiB)")
    lines.append("")
    lines.append("Docker Desktop Linux VM (aarch64, golang:1.26-alpine). Data on tmpfs or named volume only; never a macOS bind mount. Commit: `%s` (all runs). Absolute numbers are NEVER compared against spec §11 (UNVERIFIED). Fresh payloads only; dedup not in play by construction (all payloads distinct via counter; verified distinct ChunkIDs == ops)." % vol.get("commit_sha",""))
    lines.append("")
    lines.append("Outer (repro) commands (Makefile): `docker compose run --rm -e COMMIT_SHA=<sha> bench go run ./cmd/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume` and `... -dir /bench/tmpfs/sweep ... -volume-type tmpfs`. Per-row `Command` is the in-container argv.")
    lines.append("")
    lines.append("Throughput is a fraction of the measured fsync floor (`make bench-floor` on the same volume type). fsyncs/entry = 6 (3 voters x pack Sync + dir Sync). CPU/allocs exclude the fsync wait (wall time).")
    lines.append("")
    for env in (vol, tmp):
        floor = floors[env["volume_type"]]
        lines.append(f"## Volume type `{env['volume_type']}` (`{env['data_dir']}`, floor {floor:.1f} ops/s)")
        lines.append(f"Command: `{env['command']}`")
        lines.append("")
        lines.append("| Clients | ops/s | p50 ms | p99 ms | floor-fraction | alloc B/op | mallocs/op | CPU ms/op | wall s |")
        lines.append("|---|---|---|---|---|---|---|---|---|")
        for r in env["rows"]:
            frac = r["ops_per_sec"] / floor if floor else 0
            cpu = r.get("cpu_ms_per_op", 0)
            lines.append(f"| {r['clients']} | {fmt(r['ops_per_sec'],1)} | {fmt(r['p50_ms'])} | {fmt(r['p99_ms'])} | {fmt(frac,3)} | {fmt(r['alloc_bytes_per_op'],0)} | {fmt(r['mallocs_per_op'],1)} | {fmt(cpu,3)} | {fmt(r['wall_secs'],1)} |")
        lines.append("")
    lines.append("## tmpfs-vs-volume gap")
    lines.append("")
    lines.append("| Clients | tmpfs ops/s | volume ops/s | gap (tmpfs/volume) |")
    lines.append("|---|---|---|---|")
    tmp_rows = {r["clients"]: r for r in tmp["rows"]}
    for r in vol["rows"]:
        t = tmp_rows.get(r["clients"])
        gap = (t["ops_per_sec"]/r["ops_per_sec"]) if t and r["ops_per_sec"] else 0
        lines.append(f"| {r['clients']} | {fmt(t['ops_per_sec'],1)} | {fmt(r['ops_per_sec'],1)} | {fmt(gap,3)} |")
    lines.append("")
    lines.append("Higher gap = tmpfs faster (fsync on memory vs disk). Both are Docker VM storage, not reference hardware.")
    pathlib.Path("docs/benchmarks/sweep.md").write_text("\n".join(lines) + "\n")
    print("wrote docs/benchmarks/sweep.md")

if __name__ == "__main__":
    mode = sys.argv[1]
    if mode == "floor":
        render_floor(sys.argv[2], sys.argv[3])
    elif mode == "sweep":
        render_sweep(sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    else:
        sys.exit("usage: bench-render.py {floor|sweep} ...")
