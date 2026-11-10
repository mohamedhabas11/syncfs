package auth

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

// TestAddrsToSANs covers address → SAN derivation (Iter-8-06): IPv4 literals
// become IP SANs, hostnames become DNS SANs, bare hosts parse correctly.
func TestAddrsToSANs(t *testing.T) {
	dns, ips := AddrsToSANs("127.0.0.2:5001", "node2:5002")
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected 127.0.0.2 as IP SAN, got %v", ips)
	}
	if len(dns) != 1 || dns[0] != "node2" {
		t.Fatalf("expected [node2] DNS SAN, got %v", dns)
	}
	dns, ips = AddrsToSANs("localhost")
	if len(dns) != 1 || dns[0] != "localhost" {
		t.Fatalf("expected [localhost] DNS SAN, got %v", dns)
	}
	if len(ips) != 0 {
		t.Fatalf("bare hostname must not add IP SAN, got %v", ips)
	}
}

// TestCAIssueWithAddressSANs verifies a leaf issued for a non-loopback peer
// address (e.g. 127.0.0.2) carries and accepts the right SAN set.
func TestCAIssueWithAddressSANs(t *testing.T) {
	ca, err := NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("node", "n-2", []string{"node2"}, []net.IP{net.ParseIP("127.0.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.Cert.DNSNames) != 1 || leaf.Cert.DNSNames[0] != "node2" {
		t.Fatalf("expected DNS SAN node2, got %v", leaf.Cert.DNSNames)
	}
	if len(leaf.Cert.IPAddresses) != 1 || !leaf.Cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected IP SAN 127.0.0.2, got %v", leaf.Cert.IPAddresses)
	}
	if _, err := leaf.Cert.Verify(x509.VerifyOptions{
		Roots:       ca.Pool(),
		DNSName:     "127.0.0.2",
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		CurrentTime: time.Now(),
	}); err != nil {
		t.Fatalf("verify by 127.0.0.2 must pass: %v", err)
	}
}

// TestCAKeyPEMAndReload round-trips operator-held CA key material through PEM
// and re-issues a leaf under the reloaded CA (rotation prerequisite).
func TestCAKeyPEMAndReload(t *testing.T) {
	ca, err := NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := ca.KeyPEM()
	if len(pemBytes) == 0 {
		t.Fatalf("CA KeyPEM must not be empty")
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("CA KeyPEM must be an EC PRIVATE KEY PEM, got %#v", block)
	}
	ca2, err := NewCAFromPEM(ca.CertPEM(), pemBytes)
	if err != nil {
		t.Fatalf("reload CA: %v", err)
	}
	if ca2.ID != ca.ID {
		t.Fatalf("reload CA id %q != %q", ca2.ID, ca.ID)
	}
	leaf, err := ca2.Rotate("node", "n-2", []string{"node2"}, []net.IP{net.ParseIP("127.0.0.2")})
	if err != nil {
		t.Fatalf("rotate after reload: %v", err)
	}
	if leaf.Key == nil || leaf.Cert == nil {
		t.Fatalf("rotated leaf must carry key+cert")
	}
	if leaf.Cert.SerialNumber.Cmp(ca.Cert.SerialNumber) == 0 {
		t.Fatalf("rotated leaf must use a fresh serial")
	}
	if leaf.Key == nil {
		t.Fatalf("rotated leaf must mint a fresh EC key")
	}
}

// TestRotateIssuesFreshLeaf: rotation mints a new key + cert for the same
// principal; old leaf is simply superseded at restart (MVP cutover).
func TestRotateIssuesFreshLeaf(t *testing.T) {
	ca, err := NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	old, err := ca.IssueNode("n-1")
	if err != nil {
		t.Fatal(err)
	}
	newLeaf, err := ca.Rotate("node", "n-1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if newLeaf.Key == nil || newLeaf.Cert == nil {
		t.Fatalf("rotate must return leaf with key+cert")
	}
	if newLeaf.Cert.SerialNumber.Cmp(old.Cert.SerialNumber) == 0 {
		t.Fatalf("rotate must mint a fresh serial")
	}
}
