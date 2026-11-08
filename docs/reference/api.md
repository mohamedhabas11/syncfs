# API / proto reference

Wire schemas live in `api/v1/*.proto` (generated Go in `api/v1/gen/`).
Regenerate with `make proto-gen` (buf). Compatibility guard:
`make proto-breaking` (`buf breaking --against api/baseline`).

| File | Messages |
| --- | --- |
| `api/v1/common.proto` | PrincipalType, ClusterId, ProtocolVersion |
| `api/v1/commands.proto` | Command, Create/WriteRanges/Truncate/Link/Unlink/Rename/SetAttr/SetXattr/SetACL/LockGrant/LockRelease/VolumeConfig/MembershipPlan payloads, GetOperation |
| `api/v1/cluster.proto` | Cluster membership + snapshot envelope |
| `api/v1/leases.proto` | Read lease grant/revoke/ack |

Digest rule: chunk bytes never appear in Raft entries — only BLAKE3 hashes
and prepared receipts. Client operation IDs (128-bit) dedup retries.
