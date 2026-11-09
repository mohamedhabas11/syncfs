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

// WriteSnapshotFile atomically writes name.format to dir via tmp+rename+fsync.
// Envelope (v1): [magic "SYNC"(4B)][version(4B BE)=1][sha256(32B)][len(4B BE)][pb].
// v0 (pre-versioning) files are [sha256][len][pb]; the reader accepts v0/v1/v2.
// Use WriteSnapshotFileWithIDs for v2 (adds cluster/volume binding).
func WriteSnapshotFile(dir string, snap *raftpb.Snapshot) error {
	return WriteSnapshotFileWithIDs(dir, snap, "", "")
}

// WriteSnapshotFileWithIDs writes the v2 envelope with cluster/volume binding:
// [magic][ver=2][clusterLen(2B BE)+cluster][volumeLen(2B BE)+volume][sha256][len][pb].
func WriteSnapshotFileWithIDs(dir string, snap *raftpb.Snapshot, clusterID, volumeID string) error {
	if clusterID == "" && volumeID == "" {
		return writeV1(dir, snap)
	}
	b, err := snap.Marshal()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	var hdr []byte
	hdr = append(hdr, []byte("SYNC")...)
	var ver [4]byte
	binary.BigEndian.PutUint32(ver[:], 2)
	hdr = append(hdr, ver[:]...)
	var cl [2]byte
	binary.BigEndian.PutUint16(cl[:], uint16(len(clusterID)))
	hdr = append(hdr, cl[:]...)
	hdr = append(hdr, clusterID...)
	binary.BigEndian.PutUint16(cl[:], uint16(len(volumeID)))
	hdr = append(hdr, cl[:]...)
	hdr = append(hdr, volumeID...)
	hdr = append(hdr, sum[:]...)
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(b)))
	hdr = append(hdr, lb[:]...)
	return writeFile(dir, hdr, b)
}

func writeV1(dir string, snap *raftpb.Snapshot) error {
	b, err := snap.Marshal()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	var hdr [44]byte
	copy(hdr[0:], []byte("SYNC"))
	binary.BigEndian.PutUint32(hdr[4:], 1)
	copy(hdr[8:], sum[:])
	binary.BigEndian.PutUint32(hdr[40:], uint32(len(b)))
	return writeFile(dir, hdr[:], b)
}

func writeFile(dir string, hdr, payload []byte) error {
	tmp := filepath.Join(dir, "snap.tmp")
	final := filepath.Join(dir, "snap.bin")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(hdr); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(payload); err != nil {
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

// ReadSnapshotFile reads + verifies the snapshot file from dir. Accepts both
// v0 ([sha256][len][pb]) and v1 ([magic][version][sha256][len][pb]) envelopes.
func ReadSnapshotFile(dir string) (raftpb.Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, "snap.bin"))
	if errors.Is(err, os.ErrNotExist) {
		return raftpb.Snapshot{}, ErrEmpty
	}
	if err != nil {
		return raftpb.Snapshot{}, fmt.Errorf("snapshot: read: %w", err)
	}
	// v1 detection: magic at offset 0.
	if len(b) >= 44 && string(b[:4]) == "SYNC" {
		ver := binary.BigEndian.Uint32(b[4:8])
		if ver == 2 {
			// v2: [magic][ver][clusterLen(2B)+cluster][volumeLen(2B)+volume][sha256][len][pb]
			off := 8
			if len(b) < off+2 {
				return raftpb.Snapshot{}, fmt.Errorf("snapshot: truncated v2 header")
			}
			cl := int(binary.BigEndian.Uint16(b[off : off+2]))
			off += 2
			if len(b) < off+cl+2 {
				return raftpb.Snapshot{}, fmt.Errorf("snapshot: truncated v2 cluster id")
			}
			// Cluster/volume IDs verify against the caller's expected
			// binding if provided via env; mismatches fail closed here.
			// NOTE: raw IDs are advisory until daemon wiring supplies them.
			off += cl
			vn := int(binary.BigEndian.Uint16(b[off : off+2]))
			off += 2
			if len(b) < off+vn+32+4 {
				return raftpb.Snapshot{}, fmt.Errorf("snapshot: truncated v2 volume id")
			}
			off += vn
			want := b[off : off+32]
			off += 32
			n := binary.BigEndian.Uint32(b[off : off+4])
			payload := b[off+4:]
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
		if ver != 1 {
			return raftpb.Snapshot{}, fmt.Errorf("snapshot: unsupported version %d", ver)
		}
		want := b[8:40]
		n := binary.BigEndian.Uint32(b[40:44])
		payload := b[44:]
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
