// Package pki provides test-only PKI helpers (M1-03).
package pki

import (
	"crypto/x509"
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/auth"
)

// TestCA builds a cluster CA in-memory.
func TestCA(t *testing.T, clusterID string) *auth.CA {
	t.Helper()
	ca, err := auth.NewCA(clusterID)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	return ca
}

// MustIssue signs a leaf or fails the test.
func MustIssue(t *testing.T, ca *auth.CA, kind, id string) *auth.Leaf {
	t.Helper()
	var l *auth.Leaf
	var err error
	switch kind {
	case "node":
		l, err = ca.IssueNode(id)
	case "client":
		l, err = ca.IssueClient(id)
	case "admin":
		l, err = ca.IssueAdmin(id)
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return l
}

// Pool is the CA's trust pool.
func Pool(ca *auth.CA) *x509.CertPool { return ca.Pool() }
