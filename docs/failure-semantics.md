# Failure semantics

What clients see under each failure (strict profile). Evidence:
`internal/client/m3_03_test.go` (`TestSessionInvalidateAllOnDrop`,
`TestJournalPersistsAcrossRestart`, `TestReconnectFoldsServerState`).
Inv.3/4/5/6/10 are defined in `docs/architecture/index.md`.

| Failure | Client-visible behavior |
| --- | --- |
| One storage node down | nothing (quorum intact, Inv.5) |
| Leader loss | retry op-ID lookup until new leader; no guessing; `GetOperation` before retry |
| Partition minority | `EIO` after deadline; cache invalidated; no mutations, leases, locks, or uncached strict reads (Inv.5/6) |
| Quorum cannot persist chunks | `ErrNoQuorum` refused before commit (`internal/volume/volume.go:66`); no durable receipt manufactured |
| Client disconnect mid-write | outcome = unknown; Query stable op-ID before retry; evicted ops (10k entries / 10 min) surface `EIO` for reconcile, never re-execute as fresh (Inv.10) |
| Session drop | `Drop` invalidates ALL cache entries + leases before serving; FUSE connection aborted; `Check` fails closed until `Reconnect` (`internal/client/client.go:226`) |
| Client restart | durable `journal.json` reloaded (atomic tmp+fsync+rename); retained ops are folded via server lookup on `Reconnect` |
| Committed entry references missing chunk | apply deferred (Inv.4); fetch+verify then `RetryApply`; `Campaign` refused while gap persists; vote grants unaffected |
| Syncer error on write | rolled back + `EIO` (never a false durable receipt, Inv.10) |
| Released handle use | `EBADF` on Fsync/Flush after Release |
| Strict downgrade attempt | never transparently served from async or stale cache (Inv.10) |
| Corrupt snapshot (node refuses to start) | fail closed (Inv.9). Runbook: (1) stop the daemon, (2) copy the corrupt `snap.bin` to quarantine (`quarantine/snap.bin.<ts>`, never auto-delete), (3) replace the node as a **learner**: enroll, fetch a verified snapshot + chunk manifest from a healthy voter, activate atomically, replay to committed index, promote via membership change (single-change gate). Do NOT hand-edit `snap.bin` or truncate committed entries. |
| Retry after op-window eviction | retained results are bounded (**10,000 entries / 10 min** client journal; server `OpResult` by index). After eviction the duplicate is forgotten and a retry may re-execute as a fresh mutation (second apply). **Clients must surface `EIO` and reconcile (check the file/version, pick a new op-ID) — never blindly re-execute the same op-ID past the window.** |
