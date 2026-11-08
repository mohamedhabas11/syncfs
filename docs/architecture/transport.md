# Transport

QUIC + TLS 1.3 mutual auth on every connection; ALPN `syncfs/1`;
`VerifyConnection` (chain + exactly-one URI SAN + cluster binding) on
every connection including resumed ones; session tickets disabled and
0-RTT disabled (`Allow0RTT=false`, `Used0RTT` rejected). Classed
streams with local-scheduler priorities (no wire priority per RFC 9000
§2.3): Raft/Control urgency 0, Client urgency 1, Chunk/Snapshot urgency
7. Per-class framing caps: 64 KiB control, 1 MiB manifest, zero-length
frames rejected. The peer role for a class is enforced on accept;
the opener must validate its own principal before opening.
