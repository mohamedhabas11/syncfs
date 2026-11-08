package auth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"
)

func TestVerifyConnectionEnforcesCluster(t *testing.T) {
	ca, err := NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.IssueNode("n-1")
	if err != nil {
		t.Fatal(err)
	}
	fn := VerifyConnectionFunc(ca.Pool(), "cl-1")
	st := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.Cert}}
	if err := fn(st); err != nil {
		t.Fatalf("correct cluster must pass: %v", err)
	}
	fnBad := VerifyConnectionFunc(ca.Pool(), "cl-2")
	if err := fnBad(st); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("wrong cluster must fail ErrClusterMismatch, got %v", err)
	}
	fnEmpty := VerifyConnectionFunc(ca.Pool(), "cl-1")
	if err := fnEmpty(tls.ConnectionState{}); err == nil {
		t.Fatalf("empty peer certs must fail")
	}
}
