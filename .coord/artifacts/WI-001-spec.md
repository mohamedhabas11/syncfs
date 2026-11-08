# SPEC WI-001 — api/v1 Protobuf Schemas (M1-02)

**Baseline:** D-001 (FUSE + etcd/raft/v3 + QUIC `syncfs/1`). Dies if human rescinds D-001.
**Owner of this spec:** architect. **Artifact type:** interface definition (`.proto` text below IS the deliverable; generated Go is engineer's).
**Files:**
- Architect writes: `api/v1/*.proto`, `buf.yaml`, `buf.gen.yaml`, `api/v1/baseline/` (checked-in breaking-change baseline)
- Engineer writes: generated `.pb.go`, Makefile `generate` target, framing implementation if any
- Architect writes: contract tests (framing/compat as Go tests under `api/v1/*_contract_test.go`)

## Goal

Versioned Protobuf wire schemas for all SyncFS channels over QUIC ALPN `syncfs/1`, with size caps, forward/backward compatibility, and auth/cluster binding on every request.

## Messages (field numbers are normative; see `.spec/technical_spec.md` §7–8)

**Common envelope (every RPC request):** `cluster_id [16]byte=1`, `protocol_version uint32=2`, `request_id uint64=3`, `principal` (oneof: `NodeIdentity`/`ClientIdentity`/`AdminIdentity`, `uint32 role=1`, `string san_id=2`).

- **Hello/Auth (§8.1):** `Hello{cluster_id, protocol_version, role, san_id, nonce}` → `HelloAck{cluster_id, protocol_version, server_time_unix_ms}`. Mismatch on cluster/protocol → error envelope, connection closed.
- **Raft envelope:** `RaftMessage{term, index, type[AppendEntries/RequestVote/ReadIndex]}` — carried on dedicated raft streams; chunk bytes NEVER inside (spec §8.1).
- **Client ops (§7.4):** `Command{operation_id [16]byte=1, principal, preconditions []ExpectedVersion, Kind, payload (oneof: Create/WriteRanges/Truncate/Link/Unlink/Rename/SetAttr/SetXattr/SetACL/LockGrant/LockRelease), PreparedChunks []PreparedChunkReceipt, RequiredLeaseAcks []LeaseID}`. `WriteRanges{inode_id, base_manifest [32]byte, new_manifest [32]byte, ranges}`.
- **Operation status (§8.2):** `GetOperation{operation_id}` → `OperationResult{state[UNKNOWN/COMMITTED/REJECTED], raft_index, result_oneof}`.
- **Chunk receipts:** `PreparedChunkReceipt{chunk_id [32]byte, peer_id, store_generation uint64, verified_bytes uint64}`.
- **Leases:** `ReadLease{lease_id, inode_id, generation, ttl_ms}`, `LeaseAck{lease_id}` — three lease types NEVER conflated (Raft leader lease ≠ client ReadLease ≠ lock lease).
- **Snapshot:** `SnapshotHeader{format_version, raft_index, raft_term, applied_index, chunk_manifest_count, checksum [32]byte}`.
- **Membership:** `MembershipPlan{old_voters[], new_voters[], op, prev_index}` — learner-first, one change at a time.

## Error behavior

Framing: protobuf length prefix capped (control 64 KiB, chunk manifest 1 MiB, chunk bytes on dedicated streams). Oversize/wrong `cluster_id`/wrong `protocol_version` → `FAILED_PRECONDITION`/`UNAUTHENTICATED` envelope + close. Every command carries 128-bit `operation_id`; retries reuse it (idempotency §4 Inv.7).

## Failure/partition behavior

Schemas only — no server. Every request MUST include cluster binding so a node can reject cross-cluster replay without trusting payload. Unknown field numbers MUST be ignored (proto3 semantics); removed fields must be `reserved`.

## Acceptance criteria

1. `buf lint` clean; `buf breaking` passes against `api/v1/baseline/`.
2. `go test ./api/...` green under Go 1.26.0 (framing round-trip, compat reserved-field, size-cap tests).
3. Every message has envelope fields (cluster/protocol/request/principal) — contract test greps descriptors.
4. No chunk bytes inside Raft/client command payloads (only receipts/hashes).

## Contract tests (architect-owned)

- `TestContract_EnvelopeOnEveryRequest`, `TestContract_ChunkBytesNotInRaft`, `TestContract_SizeCapsEnforced`, `TestContract_ReservedCompatibility`.

## Verbatim .proto files

Architect writes them in the SPEC_REVISION if engineer's feedback requires changes; the message table above is normative for this round.
