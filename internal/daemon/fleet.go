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
// addrs are the 3 peer QUIC addresses (e.g. node1:5001). httpAddrs are the
// 3 HTTP listen addresses. DataDirs are the 3 data directories (may be empty → outDir/data-{1,2,3}).
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
	peers := []Peer{
		{ID: 1, Addr: addrs[0]},
		{ID: 2, Addr: addrs[1]},
		{ID: 3, Addr: addrs[2]},
	}
	for i := 0; i < 3; i++ {
		id := uint64(i + 1)
		leaf, err := ca.IssueNode(fmt.Sprintf("%d", id))
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
			Volume:           VolumeSpec{Name: "default", Consistency: "strict", VoterCount: 3, ChunkSizeBytes: 65536, Durability: "quorum", QuotaBytes: 214748364800, MaxFileBytes: 17179869184},
			ElectionTick:     20,
			HeartbeatTick:    5,
			RequestDeadline:  5_000_000_000,
			ShutdownTimeout:  30_000_000_000,
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
