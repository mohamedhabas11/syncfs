# Transport & Security Audit — QUIC / TLS / PKI

**Role:** Domain Agent 3 (Transport & Security: QUIC/TLS/PKI)  
**Repo:** `/Users/mohamed/syncfs`  
**Audit targets:** `.spec/technical_spec.md` §§4, 6, 8.1, 9 (+ §11 where it touches transport); `docs/architecture.md` (security boundary); `docs/operating-model.md` (air-gapped identity, enrollment, rotation); `.spec/tasks.md` M1-03, M1-04, M4-02.  
**Date:** 2026-11-08  
**Method:** Primary-source verification only (RFC text, quic-go docs/GitHub, Go `crypto/tls` pkg.go.dev + source, SPIFFE spec, cryptsetup FAQ). Every verdict below cites a URL. Excerpts are short verbatim quotes; paraphrases follow.  
**Constraint honored:** No spec files were edited. This file is the only artifact written.

> **Unified-baseline addendum (2026-11-08, research/07-design-fork-reconciliation.md §3.5/D-001):** This audit is filed against the *unified baseline*: MVP = **Camp A data path** (FUSE + `go.etcd.io/etcd/raft/v3` + quic-go QUIC push/pipeline, Pebble, BLAKE3 64 KiB chunks, quorum-durable writes, learner-first admission, one strict volume). `hashicorp/raft`, watcher/pull-based steady-state, per-directory epoch fencing, and per-directory async modes are **SUPERSEDED for MVP** (post-MVP tiers only) per D-001 and were not audited as alternatives. Camp B contributions **adopted into MVP** — node-state taxonomy (LEADER/FOLLOWER/FENCED/CANDIDATE/CATCHING_UP/QUARANTINED/HALTED), quarantine, POSIX-apply acceptance vectors, witness/learner vocabulary — are treated as operability-layer inputs to the transport/PKI findings below (see §8). The data-path fork was not re-litigated. **No D-001 challenge is raised by this domain** (see §8.3): every transport/PKI mechanism the baseline requires exists in the audited primary sources; all findings are specification gaps fixable within the baseline, not impossibility proofs.

---

## 1. Summary verdicts (read this first)

| # | Question | Verdict in one line |
|---|---|---|
| 1 | QUIC streams / HOL / prioritization | **Mostly sound, one over-claim.** QUIC *does* provide independent multiplexed streams and per-stream flow control, and loss on one stream does not head-of-line block another the way TCP does — but congestion control is connection-wide, so a bulk chunk stream still delays Raft on the *same* connection. Spec §6 is defensible; §11 “prioritized streams” over-promises because QUIC provides no wire prioritization and `quic-go` prioritization is a local-scheduler hint only. |
| 2 | TLS 1.3 over QUIC, mTLS, ALPN `syncfs/1`, identity binding | **Feasible and correctly chosen, but under-specified.** TLS 1.3 + mutual X.509 auth + ALPN `syncfs/1` is exactly what RFC 9001/8446 intend. Spec never says *which* X.509 field carries cluster identity / node ID / role — the single most important omission for M1-03/M1-04. |
| 3 | 0-RTT replay | **Disabling 0-RTT is the right call for a mutation protocol, but it does not by itself make mutations replay-safe.** Even with 0-RTT off, a retried 1-RTT mutation over a reconnected session can be a logical replay — which is why spec §4 invariant 7 (stable OperationID + bounded dedup) is load-bearing. Spec is correct to keep both; it overstates nothing *if* readers understand 0-RTT-off is necessary but not sufficient. |
| 4 | quic-go baseline | **Module path correct; Go-version claim stale.** `github.com/quic-go/quic-go` is correct. Master `go.mod` requires **Go 1.26** (`go 1.26.0`) at audit time; spec’s “Go 1.25+” is already behind for current quic-go and for its FIPS mode. Stream priority + 0-RTT controls exist but are narrower than spec implies. |
| 5 | Enrollment preauth (nonce + fingerprint in committed state) | **Sound pattern with standard precedent, not over-engineered — but the spec’s sketch is not implementable as written.** No standard PKI “supports this exact shape,” but EST/BRSKI/SVID all use the same ingredients. Gaps: bundle format, single-use atomicity, TTL, fingerprint algorithm, replay-after-use semantics. |
| 6 | LUKS/dm-crypt at-rest | **Spec’s mental model is right but incomplete.** dm-crypt/LUKS protects a powered-off stolen disk; it protects *nothing* about running memory, live snapshots, or ransomware arriving through a live mount host. Spec §9 + operating-model correctly demote it to “where required” but never state the trust-model boundary for a compromised storage host / mount host. |

---

## 2. Evidence table

> Conventions: **Sound** = claim matches primary source. **Misleading** = true in a narrower sense than spec reads. **Unsupported** = no primary source supports the claim as written. **Stale** = true at spec-writing time, now behind upstream. URLs are primary sources.

| Claim | Spec ref | URL | Excerpt / paraphrase | Verdict |
|---|---|---|---|---|
| QUIC provides multiplexed, ordered byte streams; arbitrary number concurrently subject to flow control | Spec §6, §8.1 (“Streams are classed…”) | https://www.rfc-editor.org/rfc/rfc9000.html (§2, §2.2, §4) | RFC 9000 §2: “QUIC allows for an arbitrary number of streams to operate concurrently… subject to flow control constraints and stream limits.” §2.2: “Endpoints MUST be able to deliver stream data… as an ordered byte stream.” | **Sound.** Spec’s stream classes (raft/control/client/chunk/snapshot) are legitimate application-level multiplexing. |
| QUIC eliminates TCP head-of-line blocking | Spec §6 (“no TCP head-of-line coupling”), §11 (implied isolation) | https://www.rfc-editor.org/rfc/rfc9000.html (§2: “QUIC does not provide any means of ensuring ordering between bytes on different streams.”) + https://www.rfc-editor.org/rfc/rfc9002.html (single connection congestion control) | Loss on one stream need not stall delivery on another (per-stream loss recovery), *but* congestion control is per-connection (RFC 9002), so bulk traffic still consumes the shared congestion window. QUIC removes *TCP’s* in-order stream HOL; it does not remove network/congestion coupling between streams on one connection. | **Sound in the narrow sense; Misleading if read as stream isolation.** See §3. |
| QUIC provides prioritization for Raft/lease traffic | Spec §8.1 (“transport reserves capacity for Raft and lease revocation traffic”), §11 (“separate, prioritized QUIC streams”), task M1-04 (“stream priorities”) | https://www.rfc-editor.org/rfc/rfc9000.html (§2.3) + https://quic-go.net/docs/quic/streams/ (“Stream Priorities”) + https://www.rfc-editor.org/rfc/rfc9218.html | RFC 9000 §2.3: “QUIC does not provide a mechanism for exchanging prioritization information. Instead, it relies on receiving priority information from the application… A QUIC implementation SHOULD provide ways in which an application can indicate the relative priority of streams.” quic-go docs: “Applications can set the send priority of a `quic.Stream`… `SetPriority(urgency, incremental)`. Urgency values range from 0 to 7…” RFC 9218 (urgency 0–7, incremental) is an *HTTP* prioritization scheme, not a QUIC transport wire protocol. | **Misleading.** Priority is a *local sender-side* scheduling hint (urgency/incremental mirroring RFC 9218); nothing is exchanged with the peer in RFC 9000 QUIC. There is no “reserved capacity” knob — reservation must be built at the application layer (stream-count quotas, window sizes, separate connections, or a pacing scheduler). No spec text defines which mechanism “reserves” anything. |
| QUIC has flow control (per-stream + connection) usable for backpressure | Spec §5.2 (“flow control, backpressure”), §8.1 | https://www.rfc-editor.org/rfc/rfc9000.html (§4; §4.1 MAX_DATA / MAX_STREAM_DATA) + https://quic-go.net/docs/quic/flowcontrol/ | RFC 9000 §4 defines both stream-level and connection-level flow control (MAX_STREAM_DATA / MAX_DATA) plus concurrency limits (MAX_STREAMS). quic-go exposes `InitialStreamReceiveWindow`, `MaxStreamReceiveWindow`, `MaxConnectionReceiveWindow`, `MaxIncomingStreams` in `quic.Config`. | **Sound**, but spec never sets numbers. M1-04 cannot verify “bounded Protobuf framing + flow control” without chosen window/credit values. |
| TLS 1.3 over QUIC is the mechanism (“TLS 1.3 protection”) | Spec §6, §9 | https://www.rfc-editor.org/rfc/rfc9001.html (§2.1, §3, §4.2) | RFC 9001 §2.1: “TLS provides two endpoints with a way to establish [secure communication]… After completing the TLS handshake, the client will have learned and authenticated an identity for the server, and the server is optionally able to learn and authenticate an identity for the client. TLS supports X.509 [RFC5280] certificate-based authentication for both server and client.” §4.2: “Clients MUST NOT offer TLS versions older than 1.3. An endpoint MUST terminate the connection if a version of TLS older than 1.3 is negotiated.” | **Sound.** Spec MUST also pin: require TLS 1.3 only (reject ≤1.2) — spec says “TLS 1.3” but never states the downgrade rejection. See correction C2. |
| Mutual certificate authentication on every connection | Spec §3(6), §8.1, §9 | https://www.rfc-editor.org/rfc/rfc9001.html (§4.4) + https://pkg.go.dev/crypto/tls#Config (`ClientAuth`, `ClientCAs`, `RequireAndVerifyClientCert`) | RFC 9001 §4.4: “A client MUST authenticate the identity of the server… A server MAY request that the client authenticate during the handshake. A server MAY refuse a connection if the client is unable to authenticate when requested.” Go `tls.Config`: `RequireAndVerifyClientCert` = “at least one valid certificate is required….” | **Sound, if implemented as `ClientAuth: RequireAndVerifyClientCert` + private `ClientCAs` pool on every server side and `RootCAs` = cluster CA on every client side.** Spec never names these Go settings; M1-03/M1-04 must. Post-handshake client auth is forbidden in QUIC (RFC 9001 §4.4: “A server MUST NOT use post-handshake client authentication…”), so learner/client certs must be presented inside the handshake — the spec’s enrollment flow is compatible but must be explicit about it. |
| ALPN value `syncfs/1` | Spec §8.1 | https://www.rfc-editor.org/rfc/rfc9001.html (§8.1) + https://pkg.go.dev/crypto/tls#Config (`NextProtos`) | QUIC negotiates the application protocol via TLS ALPN (RFC 7301). In Go this is `tls.Config.NextProtos = []string{"syncfs/1"}` on both endpoints; no common ALPN → handshake fails. RFC 9001 §8.1 (“Protocol Negotiation”) carries this normative requirement. | **Sound.** “`syncfs/1`” is a legitimate ALPN token. Spec must add: both ends set `NextProtos` to exactly this value, reject mismatched ALPN, no fallback. |
| “Certificates contain the expected cluster identity and node/client role” | Spec §8.1, §9 | https://www.rfc-editor.org/rfc/rfc5280.html (§4.2.1.6 SAN; §4.2.1.3 Key Usage; §4.2.1.12 EKU) + https://spiffe.io/docs/latest/spiffe-specs/x509-svid/ | RFC 5280 SAN can carry a URI (`uniformResourceIdentifier`); SPIFFE X.509-SVID profile requires **exactly one URI SAN** forming the workload identity (`spiffe://<trust-domain>/<path>`): “An X.509 SVID MUST contain exactly one URI SAN, and by extension, exactly one SPIFFE ID. Validators encountering an SVID containing more than one URI SAN MUST reject.” No RFC 5280 field is named “cluster identity and role” — identity/role binding is an application-profile decision that must be specified (URI scheme + role encoding + validator). | **Unsupported as written.** The *mechanism* (URI SAN) is standard; the *content* (“cluster identity and role”) is undefined. No field, scheme, or validation rule is specified. This is the audit’s #1 gap (see §4-G1). |
| “0-RTT application data is disabled. A reconnect must complete authentication before reads/lease renewal/mutations.” | Spec §8.1, §9; operating-model | https://www.rfc-editor.org/rfc/rfc9001.html (§4.6–4.6.3, §9.2) + https://www.rfc-editor.org/rfc/rfc8446.html (§2.3, §8, Appendix E.5) + https://raw.githubusercontent.com/quic-go/quic-go/master/interface.go (`Allow0RTT`) | RFC 8446 §2.3: “There are no guarantees of non-replay between connections.” RFC 9001 §4.6.1: 0-RTT is enabled only when the server includes the `early_data` extension with sentinel `0xffffffff` in the NewSessionTicket; omitted = “does not accept 0-RTT.” quic-go `Config.Allow0RTT bool` (server-only): “Allow0RTT allows the application to decide if a 0-RTT connection attempt should be accepted. Only valid for the server.” `DialEarly`/`ListenEarly` are the only entry points that *attempt* 0-RTT; `Dial`/`Listen` do not. | **Sound and necessary; not sufficient alone.** Default `Allow0RTT=false` + using `Dial`/`Listen` (never `DialEarly`/`ListenEarly`) achieves spec §8.1. But zero-RTT-off only closes *packet-replay of early data*; a mutation retried as ordinary 1-RTT on a new connection is still a new authenticated request — only spec §4 invariant 7 (OperationID dedup) makes that safe. See §5. |
| Session resumption allowed while 0-RTT disabled | Operating-model (“TLS session resumption may be used”) | https://www.rfc-editor.org/rfc/rfc9001.html (§4.5) + https://pkg.go.dev/crypto/tls#Config (`VerifyPeerCertificate`, `VerifyConnection`) | RFC 9001 §4.5: “Session resumption can be used to provide 0-RTT and can also be used when 0-RTT is disabled.” Go `VerifyPeerCertificate` docs: “This callback is not invoked on resumed connections… If that is not intended, use `VerifyConnection` instead, or set `SessionTicketsDisabled`.” `VerifyConnection` runs after normal verification *including resumptions* (and CVE-2025-68121’s fix made this guarantee explicit). | **Sound with a Go-specific trap.** If role/cluster validation lives only in `VerifyPeerCertificate`, it is **silently skipped on resumed connections**. Spec must mandate `VerifyConnection` (or disable tickets), or explicitly carry re-validation. Spec currently hints at resumption without this requirement. |
| quic-go module path + “Go 1.25+ … required by current QUIC dependencies” | Spec §6 | https://github.com/quic-go/quic-go + https://raw.githubusercontent.com/quic-go/quic-go/master/go.mod + https://pkg.go.dev/github.com/quic-go/quic-go | Module `github.com/quic-go/quic-go` confirmed. Master `go.mod`: `module github.com/quic-go/quic-go` / `go 1.26.0`. README: “quic-go always aims to support the latest two Go releases.” FIPS mode: “Starting with v0.60, quic-go supports use in FIPS 140-3 environments when built with Go 1.26 or newer.” | **Module path: Sound. Go-version floor: Stale / Misleading.** At audit time current quic-go needs Go 1.26; spec’s 1.25+ will fail `go mod tidy`/CI with current quic-go. M1-01 must pin a quic-go version *and* its matching Go floor (1.26 at audit time, or an older quic-go release if 1.25 must be kept). |
| quic-go stream priorities support | Spec §8.1, §11; M1-04 | https://pkg.go.dev/github.com/quic-go/quic-go (`SendStream.SetPriority`, `Stream.SetPriority`) + https://quic-go.net/docs/quic/streams/ + https://github.com/quic-go/quic-go/issues/437 | `SetPriority(urgency int8, incremental bool)` exists; `urgency` 0–7 mirrors RFC 9218 (`u=0` highest). Closed issue #437 (→ #5774, v0.62.0) confirms RFC-9218-modeled API landed only in v0.62 — i.e., “stream priorities” support is recent, version-gated, and is a local scheduler input, not QUIC wire signaling. | **Sound that the API exists (v0.62+); Misleading if spec implies peer-visible priority or capacity reservation.** M1-04 must pin quic-go ≥ v0.62 if it wants `SetPriority`. |
| quic-go 0-RTT controls | Spec §8.1; M1-04 (“0-RTT rejection”) | https://raw.githubusercontent.com/quic-go/quic-go/master/interface.go (`Allow0RTT`, `Err0RTTRejected`, `EarlyListener`) | Server: `Allow0RTT bool` + `ListenEarly` vs `Listen`. Client: `DialEarly` vs `Dial`; `ConnectionState.Used0RTT bool`. Rejected 0-RTT surfaces as `Err0RTTRejected` on stream open/read/write. | **Sound.** Disabling is `Allow0RTT=false` (default) and *not* using the `*Early` constructors. Spec/M1-04 must also assert no `DialEarly`/`ListenEarly` call sites and test `Used0RTT==false`. |
| Enrollment: “pre-authorizes a node fingerprint + one-time nonce in committed state” | Spec §9; architecture §Membership; M4-02 | https://spiffe.io/docs/latest/spiffe-specs/x509-svid/ + https://www.rfc-editor.org/rfc/rfc7030.html (§2, EST enrollment) + https://datatracker.ietf.org/doc/rfc8997/ (BRSKI one-time enrollment voucher) | No standard PKI defines “fingerprint + one-time nonce in Raft-committed state.” But the *pattern* (CA/operator pre-authorization + single-use token + proof-of-possession) is standard: EST (RFC 7030) uses operator-authorized enrollment; BRSKI (RFC 8995/8997) uses a manufacturer voucher + device-signed response; SVID issuance uses Workload-API attestation + CA issuance. Committed-state single-use tokens are analogous to one-time enrollment codes (§7) — sound for a 3-node air-gapped LAN. | **Sound pattern; Unsupported as a complete design.** See gaps G5–G7. Alternatives (SVID/SPIRE, TPM EK/AIK, EST) exist; none dominates a 3-node offline cluster — see §5-5. |
| LUKS/dm-crypt “below daemon storage where required” | Spec §9, §14-open-Q; operating-model | https://github.com/mbroz/cryptsetup/blob/main/FAQ.md (§1.2, §2.4, §5 Security Aspects) + https://man7.org/linux/man-pages/man8/cryptsetup.8.html | FAQ §1.2: “Currently it is unclear how to get LUKS or plain dm-crypt to run on [SSDs/Flash] with the full set of security assurances intact.” FAQ §2.4: LUKS “uses a metadata header and 8 key-slot areas… Passphrases are used to decrypt a single volume key.” dm-crypt/XTS protects *at-rest sectors while the kernel key is not loaded*; the FAQ’s Security Aspects section enumerates the non-goals (hostile kernel, running system, pre-boot attack). cryptsetup(8): “Plain dm-crypt encrypts the device sector-by-sector with a single, non-salted hash of the passphrase. No checks are performed…” | **Sound as a scoped statement; Incomplete as a threat model.** See §4-G9. |

---

## 3. QUIC transport analysis

### 3.1 What QUIC actually gives SyncFS

- **Independent streams, no cross-stream ordering.** RFC 9000 §2: “QUIC does not provide any means of ensuring ordering between bytes on different streams.” This is the feature spec relies on: `raft` vs `chunk` vs `control` streams can progress independently at the *stream delivery* layer.
- **Per-stream ordered delivery + per-stream loss recovery + flow control.** RFC 9000 §§2.2/4 define ordered per-stream delivery, STREAM-frame retransmission per stream, and `MAX_STREAM_DATA` / `MAX_DATA` credit. Loss of a chunk-stream packet triggers retransmission of that stream’s frames only; another stream’s buffered data is deliverable without waiting for the lost packet. In that precise sense “eliminates TCP HOL” is true.
- **What HOL elimination does *not* mean.** RFC 9002 congestion control is **per-connection**: a single congestion window (cwnd) and pacing estimate govern all streams. A 64 KiB-chunk bulk transfer and a Raft AppendEntries on the same QUIC connection share:
  - cwnd (so bulk transfer can shrink the window available to Raft),
  - pacing / probe timeout (PTO) timers,
  - packet number spaces and ACK bandwidth,
  - UDP socket buffers,
  - `InitialConnectionReceiveWindow` / `MaxConnectionReceiveWindow` (connection-level flow control).
  
  If spec §6’s phrase “no TCP head-of-line coupling” is read as “bulk chunks cannot delay Raft,” it is wrong on the same connection. The honest statement is: *QUIC removes TCP’s byte-stream HOL across streams; it leaves connection-level contention intact.* That distinction matters for §11’s latency gates (p99 ≤ 25 ms).

### 3.2 Prioritization — the most misread QUIC feature

- **RFC 9000 §2.3 is explicit and frequently misquoted:** priority is *not* a wire protocol. Endpoints do not exchange priority information; the implementation “relies on receiving priority information from the application” and “SHOULD provide ways in which an application can indicate the relative priority of streams.”
- **RFC 9218 does not change this.** `urgency (u=0..7)` + `incremental (i)` are *HTTP* priority parameters carried in the `Priority` header field / `PRIORITY_UPDATE` HTTP frames (RFC 9218 §4–§7), consumed by an HTTP server’s scheduler (§10). They are not QUIC transport frames. quic-go borrowed the *shape* (`SetPriority(urgency, incremental)`) for its local scheduler — useful, but peer-invisible.
- **quic-go history confirms recency.** Issue #437 (“Proposal for Stream Priorities,” open 2017) was closed only via #5774 in the v0.62.0 milestone with an API “modeled very closely for the needs of RFC 9218.” Before v0.62, there was effectively no supported stream-priority API. Spec §8.1/§11/M1-04 are therefore **version-gated**: they presuppose quic-go ≥ v0.62 (and, transitively, a Go floor matching that release — see §6).
- **“Transport reserves capacity for Raft and lease revocation traffic” (§8.1) names a goal with no mechanism.** Available mechanisms, in increasing strength:
  1. *Best-effort local scheduling:* `SetPriority(urgency=0, incremental=false)` for raft/lease streams, low-urgency for chunk streams. Helps packetization order; does nothing about cwnd/pacing contention because the scheduler can still only send what cwnd allows.
  2. *Flow-control shaping:* small `InitialStreamReceiveWindow`/`MaxStreamReceiveWindow` for chunk streams, larger for control streams; `MaxIncomingStreams` quotas per class (but `quic.Config` has only a *global* `MaxIncomingStreams` — per-class quotas must be enforced in `internal/transport` by counting accepts).
  3. *Separate connections:* put Raft/leases on a dedicated QUIC connection with its own cwnd/pacing (strongest isolation short of separate sockets). This is the only QUIC-native way to bound chunk interference for the §11 latency SLO.
  4. *Datagrams vs streams:* RFC 9221 datagrams (`EnableDatagrams`) are unreliable/unordered — wrong for Raft/lease revocation (need reliability), so not a substitute.
  
  **Spec must pick one** (recommendation: 1+2 as MVP, 3 as the documented SLO path). As written, M1-04 (“stream priorities”) will pass any implementation.

### 3.3 Flow control and backpressure — sound but unparameterized

RFC 9000 §4 + quic-go `Config` give SyncFS four knobs that matter: `InitialStreamReceiveWindow` (default 512 KiB), `MaxStreamReceiveWindow` (default 6 MiB), `InitialConnectionReceiveWindow` (512 KiB), `MaxConnectionReceiveWindow` (15 MiB), plus `MaxIncomingStreams`/`MaxIncomingUniStreams` (default 100). Spec §5.2 claims “flow control, backpressure” without values. For a 3-node LAN with 64 KiB chunks and 1,000 ops/s target, defaults are plausible but the chunk:control ratio needs an explicit budget or the connection window will be dominated by chunk data. M1-04 acceptance should assert concrete window values and a stream-count admission policy per class.

---

## 4. Gaps (numbered for spec fixes)

**G1 — Certificate role-validation rules missing (critical).** (§8.1, §9; M1-03)  
Spec says certs “contain the expected cluster identity and node/client role” but never defines: SAN type, URI scheme/path, single-vs-multiple-SAN handling, EKU requirements, or the validator. RFC 5280 *allows* URI SAN (§4.2.1.6); SPIFFE’s profile *requires exactly one URI SAN* (`spiffe://<trust-domain>/<path>`) and mandates rejecting certs with >1 URI SAN. Go `x509.Certificate` exposes `URIs []*url.URL`, `KeyUsage`, `ExtKeyUsage` (`serverAuth`/`clientAuth`), `Extensions` — all available for validation, but none of this is pinned. *Confirming M1-03’s verification gap:* “certificate validation + cluster-ID binding + role checks” cannot be tested without a field-level contract.

**G2 — No reservation algorithm for Raft/lease streams.** (§8.1, §11; M1-04)  
“Reserves capacity” + “prioritized streams” without a mechanism (see §3.2). Also conflicts with `quic-go`’s *global* `MaxIncomingStreams` — per-class reservation needs application-level accounting that spec never requires.

**G3 — Session resumption vs. 0-RTT vs. `VerifyPeerCertificate` trap.** (§8.1; operating-model)  
Resumption-without-0-RTT is legal (RFC 9001 §4.5) and desirable (avoids a full handshake on reconnect), but Go’s `VerifyPeerCertificate` is *not called on resumed connections*. Any role/cluster check placed there is bypassed on resume. Spec mentions resumption and role checks in neighboring paragraphs without connecting them. Must mandate `VerifyConnection` (or `SessionTicketsDisabled`) for identity re-validation.

**G4 — Rotation workflow missing.** (§9; operating-model table row)  
The operating-model’s “Certificate rotation: add new material, verify every peer, then remove old material through a committed rotation workflow” names a *committed rotation workflow* that appears nowhere else in the spec. There is no design for: overlapping trust bundles, RAFT-proposed migration of node IDs, dual-cert grace, or the interaction with membership (M4-02). M1-03 must at least define the *validation* half (reject expired / wrong-cluster / wrong-role on resume as well as full handshake).

**G5 — Enrollment bundle format undefined.** (§9; architecture §Membership; M4-02)  
“Bundle containing cluster ID, CA certificate, seed endpoint, one-time nonce, and approved node certificate fingerprint” — in what encoding (JSON? tarball? protobuf?), signed by whom (CA key? admin key?), integrity/authenticity (how does the learner know the bundle is genuine?), and secret-handling (nonce at rest? logs?). The architecture says “offline-transferable” + “approved node certificate fingerprint” but never says which hash (SHA-256 of SPKI? of `Certificate.Raw`? of `RawSubjectPublicKeyInfo`?) or how fingerprint is computed over a *future* node key pair that does not yet exist when the bundle is minted (chicken-and-egg unless the node generates its keypair first and the fingerprint is of the CSR — spec never states the order).

**G6 — Nonce single-use / replay-after-use semantics undefined.** (§9; M1-03, M4-02)  
“One-time nonce” + “single-use enrollment bundle” but no state machine: is the nonce consumed on first *connection*, first *certificate presentation*, or first *learner admission* (raft log entry)? What if the learner connects but crashes before snapshot completes — is the nonce burned (force re-mint) or retryable (replay window)? Who commits consumption or expiry, and at what Raft index? Without this, M4-02’s “one-time” test is unimplementable. Also missing: nonce entropy (spec never says 128-bit like OperationID), TTL, and whether the nonce is bound to the fingerprint (yes — should be) or independent.

**G7 — Fingerprint-preauth trust depends on committed state, but control-plane availability is unanalyzed.** (§9)  
Enrollment requires *committed* preauthorization before “learner contact” — i.e., the cluster must have quorum to admit its own replacement. On a 2-of-3 quorum-loss scenario, you cannot enroll a learner to restore quorum. Spec’s failure table never addresses this chicken-and-egg (operating-model says “Quorum lost → stop making membership changes,” which forbids exactly the recovery enrollment needs). This is a policy decision, not necessarily a bug — but it should be an explicit, documented trade-off, not an emergent property.

**G8 — ALPN edge cases unspecified.** (§8.1)  
`syncfs/1` is fine, but spec should state: `NextProtos` on *both* ends, no fallback ALPNs, and rejection behavior (handshake failure, not downgrade). QUIC version negotiation (RFC 9000 §6) is also silent — fine for MVP, but `quic.Config.Versions` should be pinned to `{Version1}` to avoid interop surprises.

**G9 — At-rest encryption trust boundary under-stated.** (§9; operating-model)  
Spec correctly scopes LUKS to “below daemon storage where required” and declares app-layer encryption out of scope — but never states what LUKS does *not* protect, which operators will assume it does: memory (daemon heap, Pebble block cache, unlocked volume key in kernel), swap, hibernation images, VM snapshots of a *running* node, and any data read by a compromised root/mount host. dm-crypt FAQ §5 enumerates these non-goals. The §9 “storage daemon trusts the authenticated mount host” paragraph is accurate for *protocol* trust but the LUKS story for a *compromised storage host* (volume key extracted from kernel memory) is never drawn. For a 3-node LAN threat model, the honest sentence is: LUKS protects a *stolen, powered-off disk*; it does not protect quorum data against a live root on a storage node.

**G10 — Mount-host trust assumption is stated but incomplete.** (§9)  
“The storage daemon trusts the authenticated mount host to report the FUSE kernel's uid/gid… A compromised root user on that host can already impersonate local users; that host is therefore inside the volume's trust boundary.” This is *accurate* for POSIX semantics (root can `setuid` before the syscall; server-side checks only stop *non-root* callers). Two clarifications are missing:
1. The bound is *per-mount-host*, not per-volume: one compromised app host → its POSIX uid/gid lies are trusted for *that host’s* sessions only; server-side ACL checks still contain the blast radius.
2. The statement does not cover a compromised *storage* host (holds volume key, Raft minority, chunk plaintext) — the quorum/2-of-3 argument contains it for *availability/consistency* but not for *confidentiality* (a single storage host sees all chunk plaintext).

**G11 — Transport error / retry semantics for M1-04.** (§8.1; M1-04)  
“Bounded Protobuf framing, deadlines, 0-RTT rejection” — but QUIC has no per-stream deadline in the transport; deadlines are app-level (`SetReadDeadline`/`SetWriteDeadline` on quic-go streams). Spec should say who enforces what (transport: handshake/idle timeouts in `quic.Config`; app: per-RPC deadlines in `internal/transport`).

---

## 5. Deep dives

### 5.1 Certificate identity profile — what “cluster identity and role” must become

RFC 5280 gives SyncFS four usable identity carriers (all verifiable with Go `crypto/x509` + `crypto/tls`):

| Candidate | Mechanism | Verdict |
|---|---|---|
| **URI SAN** (`uniformResourceIdentifier`) | `x509.Certificate.URIs` | **Recommended primary identity.** Directly follows SPIFFE X.509-SVID (§2: exactly one URI SAN). Scheme can be `spiffe://` (interoperable) or a private `syncfs://` scheme. Path encodes `cluster/<id>/node/<node-id>` or `client/<host-id>`. Single-SAN rule is enforceable (`len(URIs)==1`) and auditable. |
| **DNS SAN** | `x509.Certificate.DNSNames` | Unsuitable: implies DNS-based identity; air-gapped LAN has no DNS semantics to bind. |
| **CN** (Subject CommonName) | `x509.Certificate.Subject.CommonName` | **Must not be used.** Deprecated for client identity in modern profiles; Go’s `VerifyHostname` ignores CN when SANs exist. Spec must explicitly forbid CN-based role checks. |
| **Private X.509 extension** (e.g., `1.3.6.1.4.1.<syncfs>`) or **otherName** | `x509.Certificate.Extensions` (raw `pkix.Extension`) | Viable for structured role claims, but requires custom ASN.1 + Go parsing. Use *in addition to* URI SAN, not instead of it. |
| **EKU** (`serverAuth` / `clientAuth`) | `x509.Certificate.ExtKeyUsage` | **Good coarse role gate** (can this cert open a server vs. client connection) but too coarse for `storage-voter` vs `learner` vs `admin` vs `mount-client`. Split: EKU for transport role, URI SAN path (+ optional private ext) for cluster-role. |

**Recommended minimal contract (for M1-03 to pin):**
- Leaf cert: `KeyUsage = digitalSignature`, `ExtKeyUsage ⊇ {serverAuth, clientAuth}` (or exactly one per role; either is fine if documented), `BasicConstraintsValid`, `NotAfter` short (days, not years — air-gapped CA can re-issue offline).
- Exactly **one URI SAN**: `spiffe://syncfs.cluster/<cluster-id-uuid>/node/<node-id-hex>` (storage) or `.../client/<host-id>` (mount). Cluster ID is the §7.1 `ClusterID [16]byte` rendered as UUID/hex — *same value in cert and in every RPC header*, compared byte-for-byte after parsing.
- Optional private extension for role (`storage` | `learner` | `client` | `admin`) **or** role in URI path (simpler; recommend path). Whichever is chosen, the validator and the CA share one canonical definition.
- Go validation: `tls.Config{RootCAs: cluster, ClientCAs: cluster, ClientAuth: RequireAndVerifyClientCert, VerifyConnection: <enforce 1-URI + path + cluster + role>}`. `VerifyConnection` is mandatory (resumption trap, G3). Reject on: expired, >1 URI SAN, unknown scheme, cluster mismatch, role mismatch (e.g., `client` cert on a `raft` stream class).

### 5.2 0-RTT — why “off” is right and why it’s not enough

- **RFC 8446 §2.3 + Appendix E.5** are unambiguous: 0-RTT data “has no forward secrecy and no non-replay guarantee.” RFC 8446 §8 offers only *best-effort* anti-replay (single-use tickets, freshness windows) — global at-most-once remains a **SHOULD**, implementable only with distributed ticket-consumption tracking SyncFS does not have.
- **RFC 9001 §4.6.1** makes QUIC 0-RTT a *server-opt-in*: include `early_data` (sentinel `0xffffffff`) in the ticket or omit it. quic-go mirrors this as `Allow0RTT` (default `false`).
- **Consequence for SyncFS:** disabling 0-RTT closes the *transport* replay vector for `WriteRanges`/`Rename`/`LockGrant` mutations. It does **not** close the *application* replay vector: the same mutation, re-sent as a fresh 1-RTT request after a reconnect (or captured and re-sent by a malicious client — a compromised mount host is *inside* the trust boundary per §9, so this is not theoretical), would execute twice unless `OperationID` dedup fires. **Spec §4 invariant 7 + §8.2 step 2 are therefore mandatory, not defense-in-depth.** The spec is correct to require both; reviewers should not treat 0-RTT-off as “replay protection” in the M1-04 negative-test matrix — the test must cover *resent 1-RTT mutations with a known OperationID* vs. a *new* OperationID.

### 5.3 Go `crypto/tls` — what M1-03/M1-04 must pin

From `pkg.go.dev/crypto/tls#Config` (current stable, Go 1.27 docs):

```go
type ClientAuthType int
const (
    NoClientCert ClientAuthType = iota
    RequestClientCert
    RequireAnyClientCert
    VerifyClientCertIfGiven
    RequireAndVerifyClientCert  // ← SyncFS servers need this
)
```

- **Server side:** `ClientAuth: RequireAndVerifyClientCert`, `ClientCAs: clusterPool`, `Certificates: [<node/client cert>]`, `NextProtos: ["syncfs/1"]`, `MinVersion: tls.VersionTLS13`.
- **Client side:** `RootCAs: clusterPool`, `Certificates: [<own cert>]` (or `GetClientCertificate`), `NextProtos: ["syncfs/1"]`, `MinVersion: tls.VersionTLS13`, `InsecureSkipVerify: false` (always).
- **Identity/role enforcement:** `VerifyConnection func(ConnectionState) error` — **not** `VerifyPeerCertificate` alone (resumption skips the latter). Check `ConnectionState.PeerCertificates[0].URIs` on *every* connection, resumed or not.
- **quic-go interplay:** pass the constructed `*tls.Config` to `quic.Listen` / `quic.Dial` (`Listener`/`DialAddr`). Do **not** use `quic.ListenEarly` / `quic.DialEarly` / `Transport.ListenEarly` / `DialEarly` anywhere in the codebase (lint/grep in CI). Assert `ConnectionState.Used0RTT == false` in tests.

### 5.4 quic-go — version, surface, and what to grep for

| Aspect | Finding | Source |
|---|---|---|
| Module path | `github.com/quic-go/quic-go` | https://github.com/quic-go/quic-go |
| Current Go floor (master) | `go 1.26.0` in `go.mod` | https://raw.githubusercontent.com/quic-go/quic-go/master/go.mod |
| Release policy | “always aims to support the latest two Go releases” | https://github.com/quic-go/quic-go (README) |
| Stream priority API | `Stream.SetPriority(urgency int8, incremental bool)`; urgency 0–7, 0 highest; landed v0.62.0 (issue #437 → #5774) | https://pkg.go.dev/github.com/quic-go/quic-go + https://quic-go.net/docs/quic/streams/ + https://github.com/quic-go/quic-go/issues/437 |
| 0-RTT control | `Config.Allow0RTT bool` (server only, default `false`); early entry points `ListenEarly`/`DialEarly`; rejection surfaces as `Err0RTTRejected` | https://raw.githubusercontent.com/quic-go/quic-go/master/interface.go |
| Flow-control config | `InitialStreamReceiveWindow`, `MaxStreamReceiveWindow`, `InitialConnectionReceiveWindow`, `MaxConnectionReceiveWindow`, `MaxIncomingStreams`, `AllowConnectionWindowIncrease` | https://raw.githubusercontent.com/quic-go/quic-go/master/interface.go |
| FIPS | v0.60+ requires Go 1.26+ for FIPS 140-3 mode | https://github.com/quic-go/quic-go (README “FIPS 140-3”) |

**Practical M1-01/M1-04 pins:** choose a quic-go release (author recommends a released v0.5x tag rather than master), record its `go.mod` Go floor verbatim, and set SyncFS `go.mod` to the *max* of the two floors. If 1.25 must be kept, pin an older quic-go whose `go.mod` still says 1.25 — do not write “Go 1.25+” while depending on a 1.26-floor library.

### 5.5 Enrollment — is “fingerprint + one-time nonce in committed state” sound? What are the alternatives?

**No standard mints this exact tuple, but every ingredient is standard:**

- **Preauthorized identity + proof of possession.** EST (RFC 7030 §2) enrolls a client that the CA has *authorized* (operator-provided or SCEP challenge password); the client proves possession of its private key by signing the CSR. BRSKI (RFC 8995 + voucher RFC 8997) generalizes this: a manufacturer-signed voucher authorizes one device, the device signs its own response in the RFC 8998 format. SPIFFE/SPIRE’s Workload API attests the workload (node attestor: TPM, cloud IID, k8s SA) and mints a short-lived SVID (X.509-SVID = URI SAN, §5.1). SyncFS’s “CA-signed bundle with fingerprint + nonce” is the *offline, LAN-scale* version of the same idea: the operator (who holds the offline CA key) is the attestor.
- **Single-use nonce/token.** All three precedents use single-use, short-TTL enrollment tokens; consumption is recorded to prevent re-use. Committing the preauthorization in the Raft log is *stronger* than a CA-side spreadsheet (it survives CA workstation loss and is auditable) at the cost of the quorum chicken-and-egg (G7).
- **Fingerprint preauth (“pinning”).** Pinning the learner’s SPKI hash (SHA-256) before contact is standard TOFU-resistant enrollment (cf. `ssh-keygen -l`, JWK thumbprints RFC 7638). Spec must define *which* hash (recommend SHA-256 over `SPKI` DER, rendered hex) and *when* it is fixed (node generates keypair *before* bundle minting → admin records fingerprint → bundle embeds it → learner proves private-key possession during mTLS handshake by signing with the pinned key).

**Alternatives considered and why they don’t dominate for MVP:**
- *SPIRE / SVID rotation:* solves ongoing rotation elegantly, but requires a SPIRE server/agent per node and a Workload API — heavy for a 3-node air-gapped MVP whose CA is already offline. The *URI-SAN profile* from SPIFFE should still be adopted (§5.1) without adopting SPIRE itself.
- *TPM EK/AIK attestation:* the right long-term answer for “is this the same physical host?” but exceeds MVP hardware assumptions (spec’s reference profile is “NVMe + 10 GbE,” TPM not mentioned) and the operating-model’s offline workflow.
- *EST/BRSKI as implemented protocols:* reusable if SyncFS ever needs manufacturer-scale enrollment; unnecessary to implement the RFCs for 3–4 nodes with an operator in the loop.

**Verdict: keep the pattern; fill in the missing pieces (G5/G6).** It is not over-engineered — it is the minimal design that is both air-gapped and replay-resistant — provided single-use consumption is atomic in the Raft log and the nonce is bound to (cluster ID, fingerprint, expiry).

### 5.6 LUKS/dm-crypt — guarantees vs. non-guarantees (for §9’s honesty)

From the cryptsetup FAQ (§1.2, §2.4, §5) and `cryptsetup(8)`:

- **Guaranteed:** sector-level confidentiality (XTS mode, default `aes-xts-plain64` for >2 TiB) for bytes on the encrypted device *when the volume key is not loaded*. LUKS2 adds PBKDF (Argon2) with salting, multiple key slots, and header integrity. A stolen, powered-off disk yields ciphertext.
- **Not guaranteed (live system):** kernel memory (volume key, decrypted page cache, Pebble mmap, daemon heap), swap (unless encrypted swap is configured — FAQ §2.3), hibernation images, core dumps, VM snapshots *of a running node*, and any plaintext a live root/mount host reads after unlock. The FAQ’s running-system non-goals apply in full to a storage node: “root on the host can read the decrypted mapping.”
- **Not guaranteed (integrity/authenticity):** dm-crypt/XTS provides *no* integrity by itself. Bit flips corrupt 512-byte blocks (FAQ §4.3 documents error amplification); chunk-level BLAKE3 (spec §6) supplies integrity *above* LUKS, which is why spec §6 is correct that “BLAKE3 … QUIC/TLS remains the transport integrity layer” and LUKS is only a disk-theft control. Deletions/ransomware arriving through a *live* mount are preserved by replication itself (operating-model: “Restore from an independent backup”) — LUKS changes nothing there.
- **Not guaranteed (boot chain / evil maid):** unencrypted `/boot`/initrd, firmware modification, and unlocked-during-boot volume keys are outside dm-crypt’s boundary (FAQ §5.19/§9). TPM-bound LUKS (FAQ §2.14) mitigates *some* of this but is not in spec’s reference profile.
- **SSD caveat (FAQ §1.2):** wear-leveling / over-provisioning / TRIM behavior on NVMe means “destroyed” sectors may survive in flash; LUKS on SSDs has weaker secure-delete guarantees than on HDDs. Spec’s reference hardware *is* NVMe — worth one sentence in the threat model.

---

## 6. Concrete corrections proposed (spec-text deltas, not edits)

Each item is phrased as the sentence/pin spec should carry. Copy-ready where possible.

**C1 — Stream classes + reservation (§8.1, M1-04).** Append to §8.1: *“Raft and lease-revocation streams MUST be opened with `SetPriority(urgency=0, incremental=false)` (quic-go ≥ v0.62); chunk streams with `urgency≥5`. Per-class admission is enforced by `internal/transport` counting accepted streams against `MaxIncomingStreams` budgets (raft/control ≥ 32 reserved slots, chunk/snapshot = remainder). The spec does not rely on wire-level priority; pacing/congestion coupling is bounded by separate Raft/control connection (see §11 note).”* Add to §11: *“Latency SLOs are measured with Raft/control on a separate QUIC connection from chunk transfers.”*

**C2 — TLS floor + mTLS pinning (§9, M1-03/M1-04).** Add: *“`tls.Config.MinVersion = TLS 1.3`; negotiate `NextProtos = ["syncfs/1"]` on both ends; servers set `ClientAuth = RequireAndVerifyClientCert` with `ClientCAs` = cluster CA pool; clients set `RootCAs` = cluster pool and `InsecureSkipVerify = false`. QUIC post-handshake client authentication is not used.”*

**C3 — Identity profile (§8.1, M1-03).** Replace “Certificates contain the expected cluster identity and node/client role” with: *“Leaf certificates carry exactly one URI SAN: `spiffe://syncfs.cluster/<cluster-uuid>/node/<node-id-hex>` (storage) or `…/client/<host-id-hex>` (mount). `KeyUsage=digitalSignature`; `ExtKeyUsage ⊇ {serverAuth, clientAuth}`. Validators reject >1 URI SAN, non-`spiffe` scheme, expired / wrong-cluster / wrong-role certs. CN MUST NOT be used for identity or role.”*

**C4 — Resumption contract (§8.1, operating-model).** Append: *“Session resumption without 0-RTT is permitted. Identity/role/cluster validation MUST run in `tls.Config.VerifyConnection` (runs on resumptions), not solely in `VerifyPeerCertificate` (skipped on resumptions). The `*Early` quic-go constructors (`ListenEarly`, `DialEarly`, `Transport.ListenEarly/DialEarly`) MUST NOT be used; tests assert `ConnectionState.Used0RTT == false`.”*

**C5 — 0-RTT honesty (§8.1, §9).** Append to §9 Replay row: *“0-RTT is disabled to close transport-level early-data replay; mutation replay safety additionally requires §4 invariant 7 (OperationID dedup) for any resent authenticated request.”*

**C6 — Go/quic-go floor (§6, M1-01).** Replace “Go 1.25+” with a *pinned pair* recorded at M1-01, e.g.: *“SyncFS `go.mod` `go 1.26.0` with `github.com/quic-go/quic-go v0.6x.0` (or an older quic-go whose `go.mod` declares 1.25, if 1.25 must be retained). Stream-priority support requires quic-go ≥ v0.62.0.”* Add `grep -R "ListenEarly\|DialEarly"` to CI as a 0-RTT guard.

**C7 — Enrollment state machine (§9, M4-02).** Append the minimum contract: *“Bundle = one file, format `syncfs-enroll-v1` (protobuf): `{cluster_id, ca_cert_der, seed_addr, node_id, spki_sha256_hex, nonce[32], expires_at, admin_sig}` where `admin_sig` = ECDSA over the encoding with the offline CA key. Generation order: node creates keypair → operator records `spki_sha256` → bundle minted. The preauthorization + nonce are Raft-committed before the bundle is handed out. The nonce is consumed atomically in the same Raft entry that admits the learner (first mTLS connection presenting the pinned fingerprint consumes it); un-consumed nonces expire after 24 h; a consumed nonce is never reusable — a crashed learner requires a new bundle. See G5–G6 for the full state table.”*

**C8 — At-rest boundary (§9, operating-model).** Append: *“LUKS2 (`aes-xts-plain64`) below `/var/lib/syncfs` protects a stolen powered-off disk only. It does not protect data in memory, swap, snapshots, or data read by a live compromised host; chunk confidentiality against a live storage host is out of scope under the 2-of-3 quorum model (a single storage host sees chunk plaintext). Independent, offline, tested backups remain mandatory (ransomware/operator deletion).”*

---

## 8. Unified-baseline reconciliation (D-001, 07 §3.5/§4)

### 8.1 Baseline ratification for this domain

This report audits the baseline exactly as 07 §3.5 defines it: **FUSE (go-fuse/v2) ingress, `etcd/raft/v3` per volume, quic-go QUIC + TLS 1.3 mTLS with classed streams (`raft/control/client/chunk/snapshot`), Raft/lease-revocation prioritization, chunk bytes on dedicated streams never inside Raft entries, 0-RTT disabled (`ALPN syncfs/1`), Pebble metadata/chunk index, BLAKE3 64 KiB chunks, quorum-durable writes, learner-first admission.** §2 evidence rows for §6/§8.1/§9 already map 1:1 onto this row; no finding depends on hashicorp/raft or pull-based steady-state being available. Pull-based catch-up survives only as follower/learner replay *under commit semantics* (07 §3.5) — that replay still travels over the audited QUIC + mTLS + SAN-validated channel, so §§5.1–5.3 apply unchanged to the catch-up RPCs.

### 8.2 Camp B adoptions mapped to transport/PKI work

| Adopted (07 §3.5) | Transport/PKI consequence (owner task) |
|---|---|
| Node-state taxonomy LEADER/FOLLOWER/FENCED/CANDIDATE/CATCHING_UP/QUARANTINED/HALTED | The URI-SAN role in C3/G1 must cover at least `voter`, `learner`, `client`, `admin`. LEADER/FOLLOWER/CANDIDATE/CATCHING_UP are *Raft* states, not cert roles — a valid `voter` cert MUST be rejected from advancing state while `CATCHING_UP` (Inv.4) and campaigning while `QUARANTINED` (Inv.9). `FENCED`/`QUARANTINED`/`HALTED` nodes retain a private key until removal: spec must state that these states **MUST NOT authenticate as voters** (validator checks committed-state membership, not just cert chain) and that removal is the only key-revocation path in MVP (no CRL/OCSP for air-gapped LAN). M1-03 negative-test matrix should include “quarantined node reconnects with still-valid cert → rejected.” |
| Quarantine discipline (corrupt WAL/snapshot/chunk) | Quarantined data is daemon-private forensic evidence written *locally*, never over QUIC. Transport contract: a quarantined node stays non-voting and *cannot* trigger snapshot/chunk repair requests that other voters would honor — the “fetch from healthy peer” path (§10) must check the requester's state before serving. M4-01 must assert “quarantined node receives no snapshot.” |
| POSIX-apply vectors (mode/chown/ACL/xattr/capabilities) | No transport change (apply path stays server-side), but the authenticated principal that reaches the leader (spec §9) is the cert-derived identity. The C3 validator MUST distinguish `admin` vs `client` certs so `VolumeConfig`/`MembershipPlan` commands are never accepted from a mount-client principal. POSIX-apply tests therefore double as a role-validation test. |
| Witness/learner vocabulary | The third-node shape (full chunks vs metadata-only) is Agents 1–6 territory; the *PKI verdict* (§5.5) is that either shape enrolls identically (fingerprint-pinned learner cert + one-time nonce), differing only in which RPCs the learner's cert role authorizes. M4-02 must pin whether a `witness` cert may open `snapshot`/`chunk` streams or only `raft`/`control`. |

### 8.3 D-001 challenge assessment: none raised

Under the standing instruction (“if your domain finds a reason the baseline cannot work, report it as a D-001 challenge with primary-source evidence”), this domain **raises no challenge**. The baseline's transport/security prerequisites all exist in the audited primaries: (a) TLS 1.3 mutual X.509 auth over QUIC is the RFC 9001 normative path (§4.4); (b) 0-RTT-off is a server-side default (`Allow0RTT=false`) with `Dial`/`Listen` never attempting early data; (c) stream-priority + flow-control + stream-class multiplexing exist in quic-go (version-gated ≥v0.62, §5.4) — sufficient for the classed-stream + separate-Raft-connection design in C1 once the Go floor is pinned; (d) single-URI-SAN identity binding with Go `VerifyConnection` enforcement covers the adopted role/state vocabulary (§5.1) without SPIRE. All deficiencies found are *under-specification* (G1–G11), not impossibility. Reopening the fork from this domain would require evidence that one of (a)–(d) is false — no such evidence was found, and per tasking the data-path fork (Q1 watcher⇒no-quorum-ack proof in 07 §2) was not re-litigated.

---

## 9. Honesty note — what this audit could and could not verify

- All section-anchored IIETF excerpts were fetched as full RFC text (RFC 9000/9001/8446) and spot-checked against the normative paragraph; short quotes above are verbatim. RFC 9001 §§8.1/9.2 were verified via the RFC text TOC + surrounding normative paragraphs (ALPN-via-TLS, 0-RTT-replay) — the audit relies on those paragraphs, not on re-quoting entire sections.
- quic-go `Config` / `Allow0RTT` / `SetPriority` excerpts are from `interface.go` at `master` plus the published docs site (`quic-go.net/docs/quic/streams/`). Line numbers will drift; the *symbols and defaults* are stable across the audited window. The audit did not pin a quic-go *release tag* as “latest” because tags move; M1-01 must.
- Go `crypto/tls` excerpts are from `pkg.go.dev/crypto/tls#Config` (Go 1.27 docs). The `VerifyConnection`-on-resumption guarantee was cross-checked against the Go source/`CVE-2025-68121` fix notes; older Go docs said *both* callbacks were skipped on resumption — the current docs distinguish them. M1-03 should test against the *pinned* Go version, not the docs site.
- RFC 8446 is obsoleted by **RFC 9846** (same 0-RTT replay semantics; §2.3/§8/Appendix E.5 carried forward). Citing 8446 is still valid for the behavior under audit.
- SPIFFE X.509-SVID excerpts are from the canonical SPIFFE spec site (stable). RFC 5280 SAN rules are cited for the URI-SAN mechanism; SPIFFE adds the single-URI restriction.
- dm-crypt excerpts are from the official cryptsetup FAQ (maintained by A. Wagner / M. Broz). The FAQ’s SSD and boot-chain caveats are vendor-level cautions, not benchmarked claims — the audit reports them as documented limitations.
- No claim in this report depends on a secondary source (blog/forum) as sole evidence. Where the spec’s wording could not be verified against a primary source (e.g., “reserves capacity”), the report says so explicitly rather than speculating.

**Bottom line:** Against the unified baseline (D-001), keep QUIC + TLS 1.3 + mTLS + `syncfs/1` + 0-RTT-off + OperationID dedup + committed-state enrollment. Fix the five blanks that block M1-03/M1-04/M4-02: (1) the SAN/role validator contract, (2) the resumption-safe `VerifyConnection` rule, (3) a named stream-reservation mechanism (or separate Raft connection), (4) the enrollment bundle format + atomic single-use state machine, (5) the pinned Go/quic-go floor (1.26 at audit time). LUKS stays as scoped disk-theft protection with the live-system non-goals stated. Fetch the adopted Camp B state/POSIX-apply/witness vocabulary into the C3 role matrix so M1-03/M4-02 test them (§8.2). No D-001 challenge from this domain (§8.3).
