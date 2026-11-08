# Security policy

## Reporting a vulnerability

Email `security@TODO(human)` with the affected commit, reproduction, and
impact. SyncFS is pre-1.0 and not production ready — pretend everything is
exposed until the first release is declared.

## Scope

In scope: the `.spec/technical_spec.md` foundation (mTLS identity, quorum
durability, FUSE data path, chunk hashing). Out: external KMS/unmanaged PKI,
two-node operation, WAN replication, native-directory watchers.
