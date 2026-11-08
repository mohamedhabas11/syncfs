# Failure semantics

What clients see under each failure (strict profile). Evidence:
`internal/client/m3_03_test.go` (`TestSessionInvalidateAllOnDrop`,
`TestJournalPersistsAcrossRestart`, `TestReconnectFoldsServerState`).

| Failure | Client-visible behavior |
| --- | --- |
| One storage node down | nothing (quorum intact) |
| Leader loss | retry op-ID lookup until new leader; no guessing |
| Partition minority | `EIO` after deadline; cache invalidated |
| Client disconnect mid-write | outcome = unknown; `GetOperation` before retry |
| Session drop | `Drop` invalidates ALL cache before serving; FUSE connection aborted; `Check` fails closed until `Reconnect` |
| Client restart | durable `journal.json` reloaded; retained ops are folded via server lookup on `Reconnect` |
| Syncer error on write | rolled back + `EIO` (never a false durable receipt) |
| Released handle use | `EBADF` on Fsync/Flush after Release |
