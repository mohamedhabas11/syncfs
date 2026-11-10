// Fleet bootstrap: offline generation of CA + node identities + daemon
// configs for a 3-voter test fleet (T02 operator path).
package daemon

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohamedhabas11/syncfs/internal/auth"
)

// GenerateFleet creates cluster CA, 3 node leaves, and configs under outDir.
// addrs are the 3 peer QUIC addresses (e.g. node1:5001 or 127.0.0.2:5001).
// httpAddrs are the 3 HTTP listen addresses. DataDirs are the 3 data
// directories (may be empty → outDir/data-{1,2,3}).
//
// Iteration 8 hardening (2026-11-10): the CA key is retained on disk
// (ca.key, operator-only) to enable offline leaf rotation, and each node leaf
// is issued with the SANs derived from its dial address (IP literals become IP
// SANs, hostnames become DNS SANs) so peers can dial each other by address
// (e.g. 127.0.0.2) instead of only the loopback defaults (Iter-8-06).
func GenerateFleet(clusterID string, outDir string, addrs, httpAddrs, dataDirs []string) error {
	if clusterID == "" || outDir == "" {
		return fmt.Errorf("fleet: cluster-id and out dir required")
	}
	if len(addrs) != 3 || len(httpAddrs) != 3 {
		return fmt.Errorf("fleet: need exactly 3 peer and http addrs")
	}
	if len(dataDirs) == 0 {
		dataDirs = []string{
			filepath.Join(outDir, "data-1"),
			filepath.Join(outDir, "data-2"),
			filepath.Join(outDir, "data-3"),
		}
	}
	if len(dataDirs) != 3 {
		return fmt.Errorf("fleet: need exactly 3 data dirs")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	for _, d := range dataDirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	ca, err := auth.NewCA(clusterID)
	if err != nil {
		return err
	}
	caPath := filepath.Join(outDir, "ca.pem")
	if err := os.WriteFile(caPath, ca.CertPEM(), 0600); err != nil {
		return err
	}
	// Retain the CA private key for operator-side leaf rotation (Iter-8-06).
	if err := os.WriteFile(filepath.Join(outDir, "ca.key"), ca.KeyPEM(), 0600); err != nil {
		return err
	}
	peers := []Peer{
		{ID: 1, Addr: addrs[0]},
		{ID: 2, Addr: addrs[1]},
		{ID: 3, Addr: addrs[2]},
	}
	for i := 0; i < 3; i++ {
		id := uint64(i + 1)
		dnsNames, ips := auth.AddrsToSANs(addrs[i])
		leaf, err := ca.Issue("node", fmt.Sprintf("%d", id), dnsNames, ips)
		if err != nil {
			return err
		}
		crtPath := filepath.Join(outDir, fmt.Sprintf("node-%d.pem", id))
		keyPath := filepath.Join(outDir, fmt.Sprintf("node-%d.key", id))
		if err := os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.DER}), 0600); err != nil {
			return err
		}
		keyDER, err := x509.MarshalECPrivateKey(leaf.Key)
		if err != nil {
			return err
		}
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
			return err
		}
		cfg := Config{
			NodeID: id, ClusterID: clusterID, DataDir: dataDirs[i],
			ListenAddr: httpAddrs[i], QuicListen: addrs[i],
			Peers: peers, CACert: caPath, NodeCert: crtPath, NodeKey: keyPath,
			Volume:          VolumeSpec{Name: "default", Consistency: "strict", VoterCount: 3, ChunkSizeBytes: 65536, Durability: "quorum", QuotaBytes: 214748364800, MaxFileBytes: 17179869184},
			ElectionTick:    20,
			HeartbeatTick:   5,
			RequestDeadline: 5_000_000_000,
			ShutdownTimeout: 30_000_000_000,
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("node-%d.json", id)), b, 0600); err != nil {
			return err
		}
	}
	return nil
}

// RotateNode is the operator-side leaf rotation entry point (Iter-8-06): it
// loads the retained cluster CA (ca.pem + ca.key under outDir), issues a fresh
// node leaf for the given voter id, and rewrites that node's cert/key files.
// The CA key never leaves the operator's hands; the old leaf simply reaches its
// 825-day NotAfter before the node is restarted on the new key/cert.
func RotateNode(outDir string, id uint64, quicAddr string) (*auth.Leaf, error) {
	caCert, err := os.ReadFile(filepath.Join(outDir, "ca.pem"))
	if err != nil {
		return nil, err
	}
	caKey, err := os.ReadFile(filepath.Join(outDir, "ca.key"))
	if err != nil {
		return nil, err
	}
	ca, err := auth.NewCAFromPEM(caCert, caKey)
	if err != nil {
		return nil, err
	}
	if quicAddr == "" {
		quicAddr = fmt.Sprintf("127.0.0.1:%d", 5000+int(id-1))
	}
	dnsNames, ips := auth.AddrsToSANs(quicAddr)
	leaf, err := ca.Rotate("node", fmt.Sprintf("%d", id), dnsNames, ips)
	if err != nil {
		return nil, err
	}
	crtPath := filepath.Join(outDir, fmt.Sprintf("node-%d.pem", id))
	keyPath := filepath.Join(outDir, fmt.Sprintf("node-%d.key", id))
	if err := os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.DER}), 0600); err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(leaf.Key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return nil, err
	}
	return leaf, nil
}
