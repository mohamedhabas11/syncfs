# Known limitations

Honest register. Nothing on this list is silently dropped.

| # | Limitation | Impact | Recovery / path |
| --- | --- | --- | --- |
| 1 | xattr / ACL namespaces | PARTIAL | user.* get/set live with EOPNOTSUPP elsewhere; ACL policy enforcement pending |
| 2 | writable shared mmap | `EOPNOTSUPP` | none (excluded by design) |
| 3 | O_DIRECT explicitness | `EINVAL` in strict mode | use streaming reads |
| 4 | offline writes | unavailable | designed out (strict volume) |
| 5 | target-app qualification | deferred to M5 (ADR-0001) | needs reference hardware |
| 6 | persisted metadata index beyond Raft | in-memory scaffold | coordinator lands with M5 |
