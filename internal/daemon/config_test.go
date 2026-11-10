package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tmpConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"ca.pem": "ca", "node.pem": "crt", "node.key": "key"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	}
	return Config{
		NodeID: 1, ClusterID: "cl-1", DataDir: dir,
		ListenAddr: "127.0.0.1:0",
		QuicListen: ":0",
		Peers:      []Peer{{ID: 1, Addr: ":1"}, {ID: 2, Addr: ":2"}, {ID: 3, Addr: ":3"}},
		CACert:     filepath.Join(dir, "ca.pem"), NodeCert: filepath.Join(dir, "node.pem"), NodeKey: filepath.Join(dir, "node.key"),
		Volume:          VolumeSpec{Name: "v", Consistency: "strict", VoterCount: 3, ChunkSizeBytes: 65536, Durability: "quorum", QuotaBytes: 1 << 30, MaxFileBytes: 1 << 24},
		ElectionTick:    10,
		HeartbeatTick:   1,
		RequestDeadline: 5_000_000_000,
		ShutdownTimeout: 30_000_000_000,
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

// TestConfigLoadAcceptsStringDurations: operator-style config JSON with
// duration strings ("5s") must load (Iter-8-08: request_deadline "5s" used to
// fail because time.Duration only accepts JSON numbers).
func TestConfigLoadAcceptsStringDurations(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"ca.pem": "ca", "node.pem": "crt", "node.key": "key"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	}
	cfgJSON := `{
		"node_id": 1, "cluster_id": "cl-1", "data_dir": "` + dir + `",
		"listen_addr": "127.0.0.1:0", "quic_listen": ":0",
		"peers": [{"id":1,"addr":":1"},{"id":2,"addr":":2"},{"id":3,"addr":":3"}],
		"ca_cert": "` + filepath.Join(dir, "ca.pem") + `", "node_cert": "` + filepath.Join(dir, "node.pem") + `", "node_key": "` + filepath.Join(dir, "node.key") + `",
		"volume": {"name":"v","consistency":"strict","voter_count":3,"chunk_size_bytes":65536,"durability":"quorum","quota_bytes":1073741824,"max_file_bytes":16777216},
		"election_tick": 10, "heartbeat_tick": 1,
		"request_deadline": "5s", "shutdown_timeout": "30s"
	}`
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(cfgJSON), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig with string durations: %v", err)
	}
	if cfg.RequestDeadline != 5*time.Second {
		t.Fatalf("request_deadline = %v, want 5s", cfg.RequestDeadline)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("shutdown_timeout = %v, want 30s", cfg.ShutdownTimeout)
	}
}
