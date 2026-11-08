package auth

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	errUnknownRole     = errors.New("auth: no role for identity")
	ErrNoURISAN        = errors.New("auth: certificate must hold exactly one URI SAN")
	ErrMultipleURISAN  = errors.New("auth: certificate holds multiple URI SANs")
	ErrBadSANForm      = errors.New("auth: URI SAN must be spiffe://syncfs.cluster/<cluster-id>/<type>/<id>")
	ErrClusterMismatch = errors.New("auth: certificate cluster does not match cluster")
)

// SANPrefix is the only URI SAN shape accepted for SyncFS identity.
const SANPrefix = "spiffe://syncfs.cluster/"

// ParseSAN extracts (clusterID, type, id) from a URI SAN; error if malformed.
func ParseSAN(san string) (clusterID, principalType, id string, err error) {
	if !strings.HasPrefix(san, SANPrefix) {
		return "", "", "", ErrBadSANForm
	}
	rest := strings.TrimPrefix(san, SANPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", ErrBadSANForm
	}
	switch parts[1] {
	case "node", "client", "admin":
	default:
		return "", "", "", ErrBadSANForm
	}
	return parts[0], parts[1], parts[2], nil
}

// IdentityOf extracts exactly-one-URI-SAN identity from a leaf cert.
func IdentityOf(leaf *x509.Certificate) (Identity, error) {
	if len(leaf.URIs) == 0 {
		return Identity{}, ErrNoURISAN
	}
	if len(leaf.URIs) > 1 {
		return Identity{}, ErrMultipleURISAN
	}
	san := leaf.URIs[0].String()
	cluster, ptype, id, err := ParseSAN(san)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		ClusterID:     cluster,
		PrincipalType: ptype,
		ID:            id,
		SANURI:        san,
	}, nil
}

// NewServerTLSConfig returns a minimal TLS config requiring cluster CA verify.
// Full VerifyConnection wiring lives at the transport layer (M1-04); this
// package owns identity extraction + cluster binding only.
func NewServerTLSConfig(pool *x509.CertPool, clusterID string) (*tlsConfig, error) {
	if pool == nil || clusterID == "" {
		return nil, fmt.Errorf("auth: pool and clusterID required")
	}
	return &tlsConfig{pool: pool, clusterID: clusterID}, nil
}

// VerifyPeerCertificateFunc validates certs and binds identity to clusterID.
// It MUST be registered as VerifyConnection by the transport layer (never
// VerifyPeerCertificate alone, which is skipped on resumed connections).
// Prefer VerifyConnectionFunc below; this remains for back-compat.
func VerifyPeerCertificateFunc(pool *x509.CertPool, clusterID string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("auth: no peer certificate")
		}
		block, _ := pem.Decode(rawCerts[0])
		_ = block
		der := rawCerts[0]
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("auth: parse certificate: %w", err)
		}
		opts := x509.VerifyOptions{Roots: pool, CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		if _, err := leaf.Verify(opts); err != nil {
			return fmt.Errorf("auth: verify: %w", err)
		}
		id, err := IdentityOf(leaf)
		if err != nil {
			return err
		}
		if id.ClusterID != clusterID {
			return fmt.Errorf("%w: SAN cluster %q != %q", ErrClusterMismatch, id.ClusterID, clusterID)
		}
		return nil
	}
}

// VerifyConnectionFunc validates the connection state (chain + exactly-one
// URI SAN + cluster binding) on EVERY connection, including resumed ones.
// Transport MUST register this as tls.Config.VerifyConnection (spec §8.1).
func VerifyConnectionFunc(pool *x509.CertPool, clusterID string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("auth: no peer certificate")
		}
		leaf := cs.PeerCertificates[0]
		opts := x509.VerifyOptions{Roots: pool, CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		if _, err := leaf.Verify(opts); err != nil {
			return fmt.Errorf("auth: verify: %w", err)
		}
		id, err := IdentityOf(leaf)
		if err != nil {
			return err
		}
		if id.ClusterID != clusterID {
			return fmt.Errorf("%w: SAN cluster %q != %q", ErrClusterMismatch, id.ClusterID, clusterID)
		}
		return nil
	}
}

// DNFor returns the X.501 subject for a principal type.
func DNFor(principalType, id string) pkix.Name {
	return pkix.Name{CommonName: principalType + ":" + id, Organization: []string{"SyncFS"}}
}

type tlsConfig struct {
	pool      *x509.CertPool
	clusterID string
}
