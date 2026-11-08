# SyncFS Decisions

Autonomous build decisions. Each entry records a decision and its reasoning.

1. **09 (dependency): BLAKE3 via lukechampine.com/blake3.** The chunkstore needs
   BLAKE3-256 content addressing (spec section 6). Decision: use the pure-Go
   implementation `lukechampine.com/blake3` (x/arch-based, no CGo bind of the
   reference C code). Reason: conforms to the spec's digest requirement
   browser-testably; reference implementation is the RFC draft (I-D)
   draft-aumasson-blake3; small-input (4 KiB) cost is negligible and measured
   in the M5-01 benchmark accounting rule.
