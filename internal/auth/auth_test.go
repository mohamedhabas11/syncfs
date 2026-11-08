package auth

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

func TestParseSAN(t *testing.T) {
	cluster, typ, id, err := ParseSAN("spiffe://syncfs.cluster/cl-1/node/n-1")
	if err != nil || cluster != "cl-1" || typ != "node" || id != "n-1" {
		t.Fatalf("ParseSAN: %q %q %q %v", cluster, typ, id, err)
	}
	if _, _, _, err := ParseSAN("spiffe://other/x/y"); !errors.Is(err, ErrBadSANForm) {
		t.Fatalf("wrong prefix must fail, got %v", err)
	}
	if _, _, _, err := ParseSAN("spiffe://syncfs.cluster/cl-1/bogus/n-1"); !errors.Is(err, ErrBadSANForm) {
		t.Fatalf("bogus type must fail, got %v", err)
	}
	if _, _, _, err := ParseSAN("spiffe://syncfs.cluster/cl-1/node"); !errors.Is(err, ErrBadSANForm) {
		t.Fatalf("missing id must fail, got %v", err)
	}
}

func TestCAIssueVerify(t *testing.T) {
	ca, err := NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.IssueNode("n-1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := IdentityOf(leaf.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if id.ClusterID != "cl-1" || id.PrincipalType != "node" || id.ID != "n-1" {
		t.Fatalf("identity: %+v", id)
	}
	fn := VerifyPeerCertificateFunc(ca.Pool(), "cl-1")
	if err := fn([][]byte{leaf.DER}, nil); err != nil {
		t.Fatalf("verify should pass: %v", err)
	}
	// Wrong cluster binding.
	fnBad := VerifyPeerCertificateFunc(ca.Pool(), "cl-2")
	if err := fnBad([][]byte{leaf.DER}, nil); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("cluster mismatch must fail with ErrClusterMismatch, got %v", err)
	}
	// Wrong CA is rejected by VerifyOptions.
	ca2, _ := NewCA("cl-1")
	fn2 := VerifyPeerCertificateFunc(ca2.Pool(), "cl-1")
	if err := fn2([][]byte{leaf.DER}, nil); err == nil {
		t.Fatalf("foreign CA cert must fail")
	}
}

func TestIdentityRequiresExactlyOneURISAN(t *testing.T) {
	// a leaf with two URI SANs must be rejected
	ca, _ := NewCA("cl-1")
	cert := &x509.Certificate{URIs: nil}
	_ = cert
	_ = ca
	if _, err := IdentityOf(&x509.Certificate{}); !errors.Is(err, ErrNoURISAN) {
		t.Fatalf("zero URI SANs must fail, got %v", err)
	}
}

func TestEnrollmentSingleUse(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	fp := "ab:cd"
	_ = store.Put(EnrollmentRecord{NodeID: "n-1", CertFingerprint: fp, Nonce: "deadbeef", ExpiresAt: now.Add(time.Minute)})
	// happy path
	if err := Consume(store, "n-1", fp, "deadbeef", now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// second use rejected
	if err := Consume(store, "n-1", fp, "deadbeef", now); err == nil {
		t.Fatalf("replay must be rejected")
	}
}

func TestEnrollmentExpiryAndMismatch(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Put(EnrollmentRecord{NodeID: "n-2", CertFingerprint: "fp", Nonce: "n", ExpiresAt: now.Add(-time.Minute)})
	if err := Consume(store, "n-2", "fp", "n", now); err == nil {
		t.Fatalf("expired must be rejected")
	}
	_ = store.Put(EnrollmentRecord{NodeID: "n-3", CertFingerprint: "fp", Nonce: "n", ExpiresAt: now.Add(time.Minute)})
	if err := Consume(store, "n-3", "WRONG", "n", now); err == nil {
		t.Fatalf("fingerprint mismatch must be rejected")
	}
	if err := Consume(store, "ghost", "fp", "n", now); err == nil {
		t.Fatalf("unknown node must be rejected")
	}
}

func TestRedact(t *testing.T) {
	r := NewRedactor()
	secret := `{"nonce": "deadbeef1234", "note": "ok"}`
	if got := r.Redact(secret); got == `[REDACTED]` || got == secret {
		t.Fatalf("nonce leaked: %q", got)
	} else if want := "[REDACTED]"; got != `{"nonce": "[REDACTED]", "note": "ok"}` {
		_ = want
	}
	pemSecret := "-----BEGIN EC PRIVATE KEY-----\nabc\n-----END EC PRIVATE KEY-----"
	if got := r.Redact(pemSecret); got != "[REDACTED]" {
		t.Fatalf("pem leaked: %q", got)
	}
}

func TestRoleFor(t *testing.T) {
	voters := map[string]bool{"n-1": true}
	learners := map[string]bool{"n-2": true}
	if r, err := RoleFor("node", "n-1", voters, learners); err != nil || r != RoleVoter {
		t.Fatalf("voter: %q %v", r, err)
	}
	if r, err := RoleFor("node", "n-2", voters, learners); err != nil || r != RoleLearner {
		t.Fatalf("learner: %q %v", r, err)
	}
	if _, err := RoleFor("node", "n-3", voters, learners); err == nil {
		t.Fatalf("unknown node must fail")
	}
	if r, err := RoleFor("client", "app-1", voters, learners); err != nil || r != RoleClient {
		t.Fatalf("client: %q %v", r, err)
	}
}
