package daemon

import (
	"os"
	"path/filepath"
	"testing"
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
	for _, f := range []string{"ca.pem", "node-1.json", "node-2.json", "node-3.json", "node-1.pem", "node-1.key"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	// Config must validate (data dirs exist).
	cfg, err := LoadConfig(filepath.Join(out, "node-1.json"))
	if err != nil {
		t.Fatalf("generated config must validate: %v", err)
	}
	if cfg.NodeID != 1 || len(cfg.Peers) != 3 {
		t.Fatalf("generated config wrong: %+v", cfg)
	}
}
