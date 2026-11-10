# Security model

- Transport: TLS 1.3 over QUIC, mutual X.509 against the offline cluster CA
  (`MinVersion: tls.VersionTLS13`, `RequireAndVerifyClientCert`,
  `internal/transport/session.go:23`).
- Identity: exactly one URI SAN,
  `spiffe://syncfs.cluster/<cluster-id>/<type>/<id>` (`type` in `node`,
  `client`, `admin`); zero or multiple URI SANs rejected
  (`internal/auth/identity.go:44`).
- Verification: `VerifyConnection` (chain + SAN + cluster binding) on
  every connection including resumed ones; never `VerifyPeerCertificate`
  alone, which is skipped on resumption (`internal/auth/identity.go:108`,
  `internal/transport/session.go:34`). Session tickets are disabled.
- Replay: 0-RTT disabled (`Allow0RTT=false`, no `*Early` constructors,
  `Used0RTT` rejected) and stable 128-bit operation IDs deduplicate
  retries (`internal/transport/session.go:55`).
- Stream roles: raft/chunk/snapshot are node-only; control is node|admin;
  client is client|admin (`internal/transport/framing.go:57`). The peer
  role is enforced on accept (`AcceptClassifiedStream`); the opener must
  call `ValidatePrincipalForClass` before `OpenClassifiedStream`
  (caller duty — `internal/transport/framing.go:45`).
- At rest: LUKS/dm-crypt below the daemon (threat-model bounded in spec §9 and ADR-0001).
- Control-plane / data-plane HTTP surface: daemon loopback endpoints (`/healthz`,
  `/readyz`, `/metrics`, `/volume/*`) bind to loopback by default, require mTLS or a
  bearer token, and enforce bounded bodies; the former unauthenticated
  `POST /raft/propose` path is removed (mutations flow through the Raft quorum path
  with the section 8.2 apply barrier).
- TLS identity: node certificates MUST carry the SAN profile for every address the
  cluster actually uses (e.g. `spiffe://…/node/<id>` plus the cluster's address names,
  not only `127.0.0.1`/`localhost`), so multi-host clusters form; `GenerateFleet` MUST
  retain the offline CA key for later enrollment/replacement; leaf TTL/rotation policy
  required (825-day leaves without rotation are out of policy).
