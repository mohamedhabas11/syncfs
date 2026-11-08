package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"time"
)

// CA is the offline cluster CA (MVP: CA key never leaves operator hands).
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	DER  []byte
	ID   string // cluster ID (UUIDish; operator-chosen at pki init)
}

// NewCA creates a self-signed cluster CA cert.
func NewCA(clusterID string) (*CA, error) {
	if clusterID == "" {
		return nil, errors.New("auth: clusterID required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "syncfs-ca:" + clusterID, Organization: []string{"SyncFS"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(3650 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, DER: der, ID: clusterID}, nil
}

func (c *CA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.DER})
}

func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.Cert)
	return p
}

// Leaf is an issued identity + key material (key stays operator-scoped).
type Leaf struct {
	Cert   *x509.Certificate
	Key    *ecdsa.PrivateKey
	DER    []byte
	SANURI string
}

// IssueNode/IssueClient/IssueAdmin sign a scoped cert for one principal.
func (c *CA) IssueNode(id string) (*Leaf, error)   { return c.issue("node", id) }
func (c *CA) IssueClient(id string) (*Leaf, error) { return c.issue("client", id) }
func (c *CA) IssueAdmin(id string) (*Leaf, error)  { return c.issue("admin", id) }

func (c *CA) issue(principalType, id string) (*Leaf, error) {
	san := SANPrefix + c.ID + "/" + principalType + "/" + id
	u, err := url.Parse(san)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      DNFor(principalType, id),
		URIs:         []*url.URL{u},
		// Connection-level names only (identity = URI SAN above; the handshake
		// needs a hostname match for 127.0.0.1 test endpoints).
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, key.Public(), c.Key)
	if err != nil {
		return nil, fmt.Errorf("auth: issue %s/%s: %w", principalType, id, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Leaf{Cert: cert, Key: key, DER: der, SANURI: san}, nil
}
