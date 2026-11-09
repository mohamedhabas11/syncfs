// Package daemon is the T02 storage-daemon composition root: typed
// configuration, recovery-first startup, live readiness, and graceful
// shutdown. It is the only place that wires WAL, snapshot store, chunk
// store, metadata state machine, Raft driver, QUIC transport, volume
// coordinator, membership, repair/GC, and observability together.
package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Peer is one voter endpoint.
type Peer struct {
	ID   uint64 `json:"id"`
	Addr string `json:"addr"`
}

// VolumeSpec mirrors the persisted volume contract (spec §7.2).
type VolumeSpec struct {
	Name           string `json:"name"`
	FormatVersion  uint32 `json:"format_version"`
	Consistency    string `json:"consistency"` // MVP: strict
	VoterCount     int    `json:"voter_count"`  // MVP: exactly 3
	ChunkSizeBytes uint32 `json:"chunk_size_bytes"` // MVP: 65536 (immutable)
	Durability     string `json:"durability"`       // MVP: quorum
	QuotaBytes     uint64 `json:"quota_bytes"`
	MaxFileBytes   uint64 `json:"max_file_bytes"`
}

// Config is the validated daemon configuration. Unknown JSON fields are
// rejected so typos fail closed instead of being silently ignored.
type Config struct {
	NodeID      uint64 `json:"node_id"`
	ClusterID   string `json:"cluster_id"`
	DataDir     string `json:"data_dir"`
	ListenAddr  string `json:"listen_addr"`  // HTTP metrics/health (e.g. 127.0.0.1:8080)
	QuicListen  string `json:"quic_listen"`  // QUIC peer endpoint (e.g. :5001)
	Peers       []Peer `json:"peers"`        // all voters including self
	CACert      string `json:"ca_cert"`
	NodeCert    string `json:"node_cert"`
	NodeKey     string `json:"node_key"`
	Volume      VolumeSpec `json:"volume"`
	// Operational timeouts (all must be > 0).
	ElectionTick    int           `json:"election_tick"`    // Raft election ticks
	HeartbeatTick   int           `json:"heartbeat_tick"`   // Raft heartbeat ticks
	RequestDeadline time.Duration `json:"request_deadline"` // e.g. "5s"
	// Shutdown timeout: SIGTERM must complete within this bound.
	ShutdownTimeout time.Duration `json:"shutdown_timeout"` // e.g. "30s"
}

// LoadConfig reads and validates the daemon config file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("daemon: read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("daemon: invalid config JSON %q: %w", path, err)
	}
	// Ensure no trailing data.
	if dec.More() {
		return Config{}, fmt.Errorf("daemon: trailing JSON data in %q", path)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate enforces the production topology and fail-closed defaults.
func (c Config) Validate() error {
	if c.NodeID == 0 {
		return fmt.Errorf("daemon: node_id must be non-zero")
	}
	if c.ClusterID == "" {
		return fmt.Errorf("daemon: cluster_id is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("daemon: data_dir is required")
	}
	if info, err := os.Stat(c.DataDir); err != nil || !info.IsDir() {
		return fmt.Errorf("daemon: data_dir %q must exist and be a directory", c.DataDir)
	}
	if c.ListenAddr == "" {
		return fmt.Errorf("daemon: listen_addr is required")
	}
	if c.QuicListen == "" {
		return fmt.Errorf("daemon: quic_listen is required")
	}
	if len(c.Peers) != 3 {
		return fmt.Errorf("daemon: peers must list exactly 3 voters (got %d); production profile is three-voter strict", len(c.Peers))
	}
	foundSelf := false
	seen := map[uint64]bool{}
	for _, p := range c.Peers {
		if p.ID == 0 {
			return fmt.Errorf("daemon: peer id must be non-zero")
		}
		if seen[p.ID] {
			return fmt.Errorf("daemon: duplicate peer id %d", p.ID)
		}
		seen[p.ID] = true
		if p.Addr == "" {
			return fmt.Errorf("daemon: peer %d addr is required", p.ID)
		}
		if p.ID == c.NodeID {
			foundSelf = true
		}
	}
	if !foundSelf {
		return fmt.Errorf("daemon: node_id %d not in peers", c.NodeID)
	}
	if c.CACert == "" || c.NodeCert == "" || c.NodeKey == "" {
		return fmt.Errorf("daemon: ca_cert, node_cert, node_key are all required")
	}
	for _, p := range []string{c.CACert, c.NodeCert, c.NodeKey} {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("daemon: required file %q: %w", p, err)
		}
		if fi.IsDir() {
			return fmt.Errorf("daemon: required file %q is a directory", p)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			// Private key / CA material must not be group/world accessible.
			// Certs are not secrets but key material is; enforce strict on key.
			if p == c.NodeKey {
				return fmt.Errorf("daemon: node_key %q permissions %o too permissive (must be 0600)", p, fi.Mode().Perm())
			}
		}
	}
	if c.Volume.Name == "" {
		return fmt.Errorf("daemon: volume.name is required")
	}
	if c.Volume.Consistency != "strict" {
		return fmt.Errorf("daemon: volume.consistency must be \"strict\" (MVP)")
	}
	if c.Volume.VoterCount != 3 {
		return fmt.Errorf("daemon: volume.voter_count must be 3 (MVP)")
	}
	if c.Volume.ChunkSizeBytes != 65536 {
		return fmt.Errorf("daemon: volume.chunk_size_bytes must be 65536 (immutable format decision)")
	}
	if c.Volume.Durability != "quorum" {
		return fmt.Errorf("daemon: volume.durability must be \"quorum\"")
	}
	if c.Volume.MaxFileBytes == 0 {
		return fmt.Errorf("daemon: volume.max_file_bytes must be > 0")
	}
	if c.Volume.QuotaBytes == 0 {
		return fmt.Errorf("daemon: volume.quota_bytes must be > 0")
	}
	if c.ElectionTick <= 0 || c.HeartbeatTick <= 0 {
		return fmt.Errorf("daemon: election_tick and heartbeat_tick must be > 0")
	}
	if c.ElectionTick < 3*c.HeartbeatTick {
		return fmt.Errorf("daemon: election_tick (%d) must be at least 3x heartbeat_tick (%d)", c.ElectionTick, c.HeartbeatTick)
	}
	if c.RequestDeadline <= 0 {
		return fmt.Errorf("daemon: request_deadline must be > 0")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("daemon: shutdown_timeout must be > 0")
	}
	return nil
}

// Voters returns the voter ID list in config order.
func (c Config) Voters() []uint64 {
	out := make([]uint64, len(c.Peers))
	for i, p := range c.Peers {
		out[i] = p.ID
	}
	return out
}

// PeerAddr returns the QUIC address for a voter ID.
func (c Config) PeerAddr(id uint64) (string, bool) {
	for _, p := range c.Peers {
		if p.ID == id {
			return p.Addr, true
		}
	}
	return "", false
}

// Quorum is the majority of voters (2 of 3).
func (c Config) Quorum() int { return len(c.Peers)/2 + 1 }
