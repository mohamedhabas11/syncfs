package daemon

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/auth"
)

func TestGenerateFleet(t *testing.T) {
	out := t.TempDir()
	err := GenerateFleet("cl-test", out,
		[]string{"127.0.0.1:15001", "127.0.0.1:15002", "127.0.0.1:15003"},
		[]string{"127.0.0.1:18081", "127.0.0.1:18082", "127.0.0.1:18083"},
		nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca.pem", "ca.key", "node-1.json", "node-2.json", "node-3.json", "node-1.pem", "node-1.key"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	// The CA key must be retained on disk for offline rotation (Iter-8-06).
	caBytes, err := os.ReadFile(filepath.Join(out, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(caBytes)
	if caBlock == nil || caBlock.Type != "EC PRIVATE KEY" {
		t.Fatalf("ca.key must be an EC PRIVATE KEY PEM, got %#v", caBlock)
	}
	ca, err := auth.NewCAFromPEM(mustReadFile(t, filepath.Join(out, "ca.pem")), caBytes)
	if err != nil {
		t.Fatalf("reload fleet CA: %v", err)
	}
	if ca.ID != "cl-test" {
		t.Fatalf("fleet CA id %q != cl-test", ca.ID)
	}
	// Node leaves are issued under the retained CA; rotation must mint a
	// fresh, valid leaf (SANs for 127.0.0.1).
	leaf, err := ca.Rotate("node", "1", []string{"127.0.0.1"}, nil)
	if err != nil {
		t.Fatalf("rotate fleet node: %v", err)
	}
	if leaf.Key == nil || leaf.Cert == nil {
		t.Fatalf("fleet node leaf must carry key+cert")
	}
	if _, err := x509.ParseCertificate(leaf.DER); err != nil {
		t.Fatalf("rotated fleet leaf DER must parse: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRotateNode rewrites a fleet node's leaf + key from the retained CA
// (Iter-8-06 operator rotation path).
func TestRotateNode(t *testing.T) {
	out := t.TempDir()
	if err := GenerateFleet("cl-rot", out,
		[]string{"127.0.0.1:15001", "127.0.0.1:15002", "127.0.0.1:15003"},
		[]string{"127.0.0.1:18081", "127.0.0.1:18082", "127.0.0.1:18083"},
		nil); err != nil {
		t.Fatal(err)
	}
	leaf, err := RotateNode(out, 1, "127.0.0.1:15001")
	if err != nil {
		t.Fatalf("RotateNode: %v", err)
	}
	if leaf == nil || leaf.Cert == nil || leaf.Key == nil {
		t.Fatalf("RotateNode must return a usable leaf: %+v", leaf)
	}
	if leaf.Cert.SerialNumber.Sign() == 0 {
		t.Fatalf("rotated leaf must carry a serial")
	}
	if leaf.Key == nil {
		t.Fatalf("rotated leaf must carry a key")
	}
}
