# SPEC_REVISION WI-001 (Round 1 — answers SPEC_FEEDBACK)

**Baseline:** D-001. **Answering:** engineer SPEC_FEEDBACK (principal ambiguity, unpinned types, tooling).
**Disposition:** feedback (1) and (2) are CORRECT — fixed below. (3) agreed. (4) noted.

## Fix 1 — Principal shape (BLOCKER in feedback, now pinned)

Decision: FLAT envelope fields, NOT a oneof of structs.

```
bytes cluster_id = 1;
uint32 protocol_version = 2;
uint64 request_id = 3;
PrincipalType principal_type = 4;   // enum: UNSPECIFIED=0, NODE=1, CLIENT=2, ADMIN=3
uint32 role = 5;                    // e.g. voter/learner inside NODE
string san_id = 6;                  // spiffe://... URI, source of truth for identity
```

Rationale: uniform validation (every handler reads envelope fields), no oneof-wrapper friction in generated Go, forward-compatible (new PrincipalType values ignore safely). Supersedes the oneof sketch in the original SPEC.

## Fix 2 — Missing normative types (pinned from `.spec/technical_spec.md` §7)

```
enum CommandKind {
  COMMAND_KIND_UNSPECIFIED = 0; CREATE = 1; WRITE_RANGES = 2; TRUNCATE = 3;
  LINK = 4; UNLINK = 5; RENAME = 6; SET_ATTR = 7; SET_XATTR = 8;
  SET_ACL = 9; LOCK_GRANT = 10; LOCK_RELEASE = 11;
  VOLUME_CONFIG = 12; MEMBERSHIP_PLAN = 13;
}
message ExpectedVersion { bytes inode_id = 1; uint32 expected_revision = 2; }
message Command { bytes operation_id = 1; /* envelope fields 4-6 as above */
  repeated ExpectedVersion preconditions = 7; CommandKind kind = 8;
  bytes payload = 9; /* oneof payload per Kind, versioned */ }
message PreparedChunkReceipt { bytes chunk_id = 1; bytes peer_id = 2;
  uint64 store_generation = 3; uint64 verified_bytes = 4; }
message Lease { bytes lease_id = 1; bytes inode_id = 2; uint32 generation = 3; uint64 ttl_ms = 4; }
message OperationResult { enum State { UNKNOWN=0; COMMITTED=1; REJECTED=2; } State state = 1;
  uint64 raft_index = 2;
  oneof result { WriteResult write = 3; ErrorResult error = 4; } }
message WriteResult { bytes new_manifest = 1; }
message ErrorResult { string code = 1; string message = 2; }
message NodeRef { bytes node_id = 1; string san_id = 2; }
message MembershipPlan { repeated NodeRef old_voters = 1; repeated NodeRef new_voters = 2;
  uint64 op = 3; uint64 prev_index = 4; }
```

## Fix 3 — Tooling (agreed)

`buf lint`, `buf breaking` vs `api/v1/baseline/`, `buf generate` into `api/v1/gen/` (engineer wires Makefile `generate`). Proto files under `api/v1/`, Go package `syncfs.api.v1`.

## Updated acceptance

- (a) Principal flat-shape in descriptors (contract test asserts fields 4–6 present, no oneof).
- (b) All fixed types above present with exact numbers; `CommandKind` has 14 values (0–13).
- Others unchanged from original SPEC. Architect writes `.proto` + `buf.yaml` + `baseline/` + contract tests in the final revision; engineer's CLAIM+IMPLEMENTED covers generated Go + Makefile generate + `go test ./api/...` green.

Objection window: engineer says proceed to CLAIM or second feedback round.
