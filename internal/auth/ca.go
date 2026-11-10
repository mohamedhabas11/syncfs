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
	"strings"
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

// KeyPEM exports the CA private key in PEM form. The key never leaves the
// operator's hands in production; this export exists so an operator-side tool
// (fleet bootstrap / offline leaf rotation) can retain and re-sign with it.
func (c *CA) KeyPEM() []byte {
	keyDER, err := x509.MarshalECPrivateKey(c.Key)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// NewCAFromPEM loads an existing cluster CA from operator-provided PEM
// material (cert + key). Used by offline rotation flows: the CA key is retained
// by the operator (ca.key) and a node leaf is re-issued under it.
func NewCAFromPEM(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, errors.New("auth: ca: no certificate in pem")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: ca: parse certificate: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("auth: ca: no key in pem")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: ca: parse key: %w", err)
	}
	id := strings.TrimPrefix(cert.Subject.CommonName, "syncfs-ca:")
	if id == "" || id == cert.Subject.CommonName {
		// Fallback for CNs we did not mint; require an explicit cluster id at
		// the call sites that can supply one.
		id = ""
	}
	return &CA{Cert: cert, Key: key, DER: cert.Raw, ID: id}, nil
}

// AddrsToSANs converts dial addresses ("host:port" or bare "host") into the
// connection-level SAN set for a leaf: IPv4/IPv6 literals become IP SANs,
// hostnames become DNS SANs. Used by fleet bootstrap so node leaves carry the
// SANs matching the addresses peers actually dial (e.g. 127.0.0.2 for a second
// voter) instead of only the loopback defaults (2026-11-10 execution review,
// Iter-8-06).
func AddrsToSANs(addrs ...string) (dnsNames []string, ips []net.IP) {
	for _, a := range addrs {
		host := a
		if h, _, err := net.SplitHostPort(a); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, ip)
			continue
		}
		if host == "" || strings.Contains(host, ":") {
			continue
		}
		dnsNames = append(dnsNames, host)
	}
	return dnsNames, ips
}

// Leaf is an issued identity + key material (key stays operator-scoped).
type Leaf struct {
	Cert   *x509.Certificate
	Key    *ecdsa.PrivateKey
	DER    []byte
	SANURI string
}

// Issue signs a scoped leaf for principalType/id bound to connection-level
// SANs (dnsNames + ips). The identity (URI SAN) always binds the principal to
// this cluster CA regardless of the connection-level names.
func (c *CA) Issue(principalType, id string, dnsNames []string, ips []net.IP) (*Leaf, error) {
	if principalType == "" || id == "" {
		return nil, errors.New("auth: principal type and id required")
	}
	if len(dnsNames) == 0 {
		dnsNames = []string{"localhost"}
	}
	if len(ips) == 0 {
		ips = []net.IP{net.ParseIP("127.0.0.1")}
	}
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
		// needs a hostname/IP match for the endpoints peers actually dial).
		DNSNames:    dnsNames,
		IPAddresses: ips,
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

// IssueNode/IssueClient/IssueAdmin sign a scoped cert for one principal with
// default loopback SANs.
func (c *CA) IssueNode(id string) (*Leaf, error)   { return c.Issue("node", id, nil, nil) }
func (c *CA) IssueClient(id string) (*Leaf, error) { return c.Issue("client", id, nil, nil) }
func (c *CA) IssueAdmin(id string) (*Leaf, error)  { return c.Issue("admin", id, nil, nil) }

// Rotate issues a fresh leaf (new key pair) for an existing principal. The CA
// retains its key offline; the operator swaps the node's cert/key before the
// old leaf's NotAfter (825 days) lapses. Rotation does not revoke the old leaf
// yet (post-MVP monotonic-id scheme); key/cert replacement + restart is the
// MVP cutover.
func (c *CA) Rotate(principalType, id string, dnsNames []string, ips []net.IP) (*Leaf, error) {
	return c.Issue(principalType, id, dnsNames, ips)
}
