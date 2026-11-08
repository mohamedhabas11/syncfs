# Write path

Quorum-durable chunks precede the metadata commit (spec §8.2).

```mermaid
sequenceDiagram
    actor App
    participant Fuse as syncfs mount
    participant Leader as volume leader
    participant Quorum as storage majority (2/3)
    App->>Fuse: write/rename/fsync
    Fuse->>Leader: stable OpID
    Leader->>Quorum: stage 64KiB COW chunks
    Quorum-->>Leader: durable receipts
    Leader->>Leader: revoke conflicting leases
    Leader->>Leader: propose preconditioned metadata to Raft
    Leader->>Leader: wait majority commit
    Leader-->>Fuse: apply to state machine
    Fuse-->>App: success; invalidate cache
```
