package metadata

import (
	"os"
	"path/filepath"
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
)

func TestPersistRoundTrip(t *testing.T) {
	m := New()
	// Create a file via Apply (deterministic ID path).
	var opID [16]byte
	opID[0] = 1
	cmd := &syncfsapi.Command{
		OperationId: opID[:],
		Kind:        syncfsapi.CommandKind_CREATE,
		Payload: &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), // root
			Name:          "f",
		}},
	}
	if err := m.Apply(opID, 1, cmd); err != nil {
		t.Fatalf("apply create: %v", err)
	}
	dir := t.TempDir()
	if err := m.Persist(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPersisted(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Lookup([16]byte{}, "f"); !ok {
		t.Fatalf("dentry f must survive persist round-trip")
	}
	// Corrupt file must fail closed.
	_ = os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"version":1,"checksum":"bad"}`), 0600)
	if _, err := LoadPersisted(dir); err == nil {
		t.Fatalf("corrupt persist must fail")
	}
}
