# Read and lease

Strict userspace cache with server leases; a drop of the control
connection invalidates everything (Inv.6). Three leases are distinct:
Raft leader lease (ReadIndex), client ReadLease (cache authority), and
POSIX lock lease (state-machine record).

```mermaid
flowchart TD
    Fresh[cache miss / expired lease] --> RI[ReadIndex with ReadOnlySafe or lease revalidate]
    RI -->|lease valid and generation current| Hit[serve userspace cache]
    RI -->|no lease| Leader[leader grants ReadLease at generation]
    Leader --> Cached[cache + serve]
    Leader --> Inv[conflicting write: revoke + WaitClear ack or expiry before commit]
    Drop[control connection lost] --> DropAll[invalidate ALL entries and leases]
```
