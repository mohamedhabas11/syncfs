package metadata

import (
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
)

func rootExt() [16]byte { return [16]byte{} }

func TestCreateThenLookup(t *testing.T) {
	m := New()
	c := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "foo"},
	}}
	if err := m.Apply([16]byte{1}, 1, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := m.Lookup(rootExt(), "foo"); !ok {
		t.Fatalf("foo must exist after create")
	}
}

func TestAtomicityOnError(t *testing.T) {
	m := New()
	bad := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: ""},
	}}
	if err := m.Apply([16]byte{9}, 1, bad); err == nil {
		t.Fatalf("empty name must fail")
	}
	if _, ok := m.Lookup(rootExt(), ""); ok {
		t.Fatalf("failed create must leave no trace")
	}
	r, ok := m.GetOp([16]byte{9})
	if !ok || r.State != "REJECTED" {
		t.Fatalf("failed op must be REJECTED: %+v", r)
	}
}

func TestIdempotentOpID(t *testing.T) {
	m := New()
	c := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "x"},
	}}
	if err := m.Apply([16]byte{7}, 1, c); err != nil {
		t.Fatal(err)
	}
	// same opID again: must be refused as duplicate, not a second mutation
	if err := m.Apply([16]byte{7}, 2, c); err == nil {
		t.Fatalf("duplicate opID must be refused")
	}
	if _, ok := m.Lookup(rootExt(), "x"); !ok {
		t.Fatalf("original must still be applied exactly once")
	}
}

func TestPreconditionStaleFails(t *testing.T) {
	m := New()
	c := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "y"},
	}}
	if err := m.Apply([16]byte{1}, 1, c); err != nil {
		t.Fatal(err)
	}
	id, ok := m.Lookup(rootExt(), "y")
	if !ok {
		t.Fatal("y missing")
	}
	w := &syncfsapi.Command{Kind: syncfsapi.CommandKind_TRUNCATE, Payload: &syncfsapi.Command_Truncate{
		Truncate: &syncfsapi.TruncatePayload{InodeId: id[:], Size: 5},
	}}
	w.Preconditions = []*syncfsapi.ExpectedVersion{{InodeId: id[:], ExpectedRevision: 99}}
	if err := m.Apply([16]byte{2}, 2, w); err == nil {
		t.Fatalf("stale precondition must fail (lost update)")
	}
	if e := m.inodes[id]; e.Rev != 1 {
		t.Fatalf("rev must be unchanged after stale failure, got %d", e.Rev)
	}
}

func TestRenameMovesAtomically(t *testing.T) {
	m := New()
	c := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "a"},
	}}
	if err := m.Apply([16]byte{1}, 1, c); err != nil {
		t.Fatal(err)
	}
	rn := &syncfsapi.Command{Kind: syncfsapi.CommandKind_RENAME, Payload: &syncfsapi.Command_Rename{
		Rename: &syncfsapi.RenamePayload{SrcParent: make([]byte, 16), SrcName: "a", DstParent: make([]byte, 16), DstName: "b"},
	}}
	if err := m.Apply([16]byte{2}, 2, rn); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup(rootExt(), "a"); ok {
		t.Fatalf("old name must be gone")
	}
	if _, ok := m.Lookup(rootExt(), "b"); !ok {
		t.Fatalf("new name must exist")
	}
}
