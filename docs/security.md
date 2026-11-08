# Security model

- Transport: TLS 1.3 over QUIC, mutual X.509 against the offline cluster CA.
- Identity: exactly one URI SAN, `spiffe://syncfs.cluster/<cluster>/<type>/<id>`.
- Replay: 0-RTT disabled (`Allow0RTT=false` + no `*Early` constructors); `VerifyPeerCertificate` runs on every handshake (tickets disabled where needed).
- At rest: LUKS/dm-crypt below the daemon (threat-model bounded in spec §9 and ADR-0001).
