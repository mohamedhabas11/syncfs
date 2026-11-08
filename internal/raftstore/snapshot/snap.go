// Package snapshot persists raft.pb.Snapshot checkpoints with checksums (M2-01).
package snapshot

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

var (
	ErrChecksumMismatch = errors.New("snapshot: checksum mismatch")
	ErrEmpty            = errors.New("snapshot: no snapshot file")
)

// WriteSnapshotFile atomically writes name.format: [sha256(32B)][len(4B)][pb bytes]
// to dir via tmp+rename+fsync(file)+fsync(dir).
func WriteSnapshotFile(dir string, snap *raftpb.Snapshot) error {
	b, err := snap.Marshal()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	var hdr [36]byte
	copy(hdr[0:], sum[:])
	binary.BigEndian.PutUint32(hdr[32:], uint32(len(b)))
	tmp := filepath.Join(dir, "snap.tmp")
	final := filepath.Join(dir, "snap.bin")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(hdr[:]); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	// fsync the containing dir so the rename itself is durable.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// ReadSnapshotFile reads + verifies the snapshot file from dir.
func ReadSnapshotFile(dir string) (raftpb.Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, "snap.bin"))
	if errors.Is(err, os.ErrNotExist) {
		return raftpb.Snapshot{}, ErrEmpty
	}
	if err != nil {
		return raftpb.Snapshot{}, fmt.Errorf("snapshot: read: %w", err)
	}
	if len(b) < 36 {
		return raftpb.Snapshot{}, fmt.Errorf("snapshot: file too short (%d bytes)", len(b))
	}
	want := b[:32]
	n := binary.BigEndian.Uint32(b[32:36])
	payload := b[36:]
	if uint32(len(payload)) != n {
		return raftpb.Snapshot{}, fmt.Errorf("snapshot: length field %d != %d bytes", n, len(payload))
	}
	got := sha256.Sum256(payload)
	if hex.EncodeToString(got[:]) != hex.EncodeToString(want) {
		return raftpb.Snapshot{}, ErrChecksumMismatch
	}
	var snap raftpb.Snapshot
	if err := snap.Unmarshal(payload); err != nil {
		return raftpb.Snapshot{}, fmt.Errorf("snapshot: unmarshal: %w", err)
	}
	return snap, nil
}
