package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func tmpConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"ca.pem": "ca", "node.pem": "crt", "node.key": "key"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	}
	return Config{
		NodeID: 1, ClusterID: "cl-1", DataDir: dir,
		ListenAddr:  "127.0.0.1:0",
		QuicListen:  ":0",
		Peers:       []Peer{{ID: 1, Addr: ":1"}, {ID: 2, Addr: ":2"}, {ID: 3, Addr: ":3"}},
		CACert: filepath.Join(dir, "ca.pem"), NodeCert: filepath.Join(dir, "node.pem"), NodeKey: filepath.Join(dir, "node.key"),
		Volume:           VolumeSpec{Name: "v", Consistency: "strict", VoterCount: 3, ChunkSizeBytes: 65536, Durability: "quorum", QuotaBytes: 1 << 30, MaxFileBytes: 1 << 24},
		ElectionTick:     10,
		HeartbeatTick:    1,
		RequestDeadline:  5_000_000_000,
		ShutdownTimeout:  30_000_000_000,
	}
}

func TestConfigRejectsUnknownFieldsLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(p, []byte(`{"node_id":1,"unknown_field":123}`), 0600)
	if _, err := LoadConfig(p); err == nil {
		t.Fatalf("unknown field must be rejected")
	}
}

func TestConfigValidateRequiresThreeVoters(t *testing.T) {
	cfg := tmpConfig(t)
	cfg.Peers = cfg.Peers[:2]
	if err := cfg.Validate(); err == nil {
		t.Fatalf("two voters must be rejected for three-voter MVP")
	}
}

func TestConfigLoadRoundTrip(t *testing.T) {
	cfg := tmpConfig(t)
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "cfg.json")
	_ = os.WriteFile(p, b, 0600)
	// DataDir inside cfg still points at tmpConfig dir; validation needs that dir to exist (it does).
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig round-trip: %v", err)
	}
	if got.NodeID != 1 || got.ClusterID != "cl-1" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
