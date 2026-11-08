# Failure semantics

What clients see under each failure (strict profile).

| Failure | Client-visible behavior |
| --- | --- |
| One storage node down | nothing (quorum intact) |
| Leader loss | retry op-ID lookup until new leader; no guessing |
| Partition minority | `EIO` after deadline; cache invalidated |
| Client disconnect mid-write | outcome = unknown; `GetOperation` before retry |
