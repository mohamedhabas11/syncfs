# Runbooks (VM test fleet)

## No quorum (`/readyz` 503)
- Check `syncfs admin status --endpoint <node>` for all 3. If ≤1 ready, restore voters or follow disaster-recovery (do not edit WAL/state dirs).

## Failed leader
- `curl /metrics` for `syncfs_leader_quorum`; leader should re-elect within `election_tick * tick_interval` (500ms–2s). If not, check peer QUIC reachability (`/raft/propose` logs) and `systemd-analyze security` for blocked UDP.

## Node replacement
- Bootstrap replacement as learner, verify snapshot (`daemon.Snapshot()`), join via Raft `ConfChange`, drain old member last. Never remove a healthy voter first.

## Disk exhaustion / corruption
- Keep node non-voting, preserve `/var/lib/syncfs` for forensics, rebuild from snapshot. `syncfs admin status` + `docs/known-limitations.md #9` recovery.

## Cert rotation
- Add new trust material, verify every peer (`VerifyConnectionFunc`), then remove old via committed workflow. Expired creds are rejected (T05).

## Snapshot restore
- Restore verified snapshot + WAL tail (`LoadPersisted` + replay). Corrupt snapshot fails closed — quarantine and rebuild.

## Upgrade / rollback
- Only N→N+1 with format version bump + ADR. Never downgrade persisted format without tested reverse migration. Release artifacts need checksums + SBOM + signature (pending T11).

## Emergency read-only
- Stop mounts (`syncfs mount` is fail-closed without coordinator). Reads after lease invalidate must fail with `EIO`/`ENOTCONN`, never stale.
