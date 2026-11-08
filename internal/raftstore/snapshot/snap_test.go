package snapshot

import (
	"os"
	"testing"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

func readAll(t *testing.T, p string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(p)
}

func writeAll(t *testing.T, p string, b []byte) error {
	t.Helper()
	return os.WriteFile(p, b, 0600)
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	snap := &raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 7, Term: 3}}
	if err := WriteSnapshotFile(dir, snap); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshotFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Index != 7 || got.Metadata.Term != 3 {
		t.Fatalf("round trip: %+v", got.Metadata)
	}
}

func TestReadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadSnapshotFile(dir); err == nil {
		t.Fatalf("missing snapshot must error")
	}
	snap := &raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1}}
	if err := WriteSnapshotFile(dir, snap); err != nil {
		t.Fatal(err)
	}
	// corrupt the payload
	p := dir + "/snap.bin"
	b, _ := readAll(t, p)
	b[len(b)-1] ^= 0xff
	_ = writeAll(t, p, b)
	if _, err := ReadSnapshotFile(dir); err == nil {
		t.Fatalf("corrupt snapshot must fail checksum")
	}
}
