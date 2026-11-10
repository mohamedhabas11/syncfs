// Package transport implements mTLS QUIC sessions with syncfs/1 ALPN,
// classed prioritized streams, and bounded protobuf framing (M1-04).
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

	"github.com/mohamedhabas11/syncfs/internal/auth"
	"github.com/quic-go/quic-go"
)

// ALPN is the only accepted application protocol (spec 8.1).
const ALPN = "syncfs/1"

// ServerTLSConfig builds a *tls.Config for QUIC endpoints: TLS 1.3 only
// (min version), mutual auth via cluster CA, session tickets disabled so
// peer verification cannot be skipped on resumption, 0-RTT off.
func ServerTLSConfig(certs []tls.Certificate, pool *x509.CertPool, clusterID string) (*tls.Config, error) {
	return &tls.Config{
		Certificates: certs,
		NextProtos:   []string{ALPN},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		RootCAs:      pool,
		// VerifyConnection (not VerifyPeerCertificate, which is skipped on
		// resumed connections) enforces chain + SAN + cluster on every
		// connection, per spec §8.1.
		VerifyConnection:       auth.VerifyConnectionFunc(pool, clusterID),
		SessionTicketsDisabled: true,
	}, nil
}

// ClientTLSConfig is the same config for dialing (clients present certs,
// servers require them; both sides verify identity).
func ClientTLSConfig(certs []tls.Certificate, pool *x509.CertPool, clusterID string) (*tls.Config, error) {
	return &tls.Config{
		Certificates:           certs,
		NextProtos:             []string{ALPN},
		MinVersion:             tls.VersionTLS13,
		ClientAuth:             tls.RequireAndVerifyClientCert,
		ClientCAs:              pool,
		RootCAs:                pool,
		VerifyConnection:       auth.VerifyConnectionFunc(pool, clusterID),
		SessionTicketsDisabled: true,
	}, nil
}

// Listen starts a QUIC endpoint that never uses 0-RTT (server Allow0RTT=false is the quic default).
func Listen(addr string, tlsConf *tls.Config, cfg *quic.Config) (*quic.Listener, error) {
	if cfg == nil {
		cfg = &quic.Config{Allow0RTT: false}
	}
	if cfg.Allow0RTT {
		return nil, errors.New("transport: Allow0RTT must stay false")
	}
	return quic.ListenAddr(addr, tlsConf, cfg)
}

// Dial connects over UDP with strict auth and no 0-RTT attempt.
// It NEVER calls DialEarly; Used0RTT is false by construction.
func Dial(ctx context.Context, addr string, tlsConf *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
	if cfg == nil {
		cfg = &quic.Config{Allow0RTT: false}
	}
	if cfg.Allow0RTT {
		return nil, errors.New("transport: Allow0RTT must stay false")
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: resolve %s: %w", addr, err)
	}
	// Unconnected packet conn (quic must not see a connected socket).
	pc, err := net.ListenPacket("udp", "")
	if err != nil {
		return nil, fmt.Errorf("transport: listen udp: %w", err)
	}
	conn, err := quic.Dial(ctx, pc, udpAddr, tlsConf, cfg)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("transport: quic dial: %w", err)
	}
	if conn.ConnectionState().Used0RTT {
		_ = conn.CloseWithError(0, "0-rtt rejected")
		return nil, errors.New("transport: 0-RTT was used; refusing session")
	}
	return conn, nil
}
