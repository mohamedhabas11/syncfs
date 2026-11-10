package metadata

import (
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
)

// TestDeterministicReplay verifies that applying the same committed log on
// two FSMs yields identical state. Op retention is Raft-index-ordered (no
// wall clock), so replay is a pure function of (opID, idx, cmd).
func TestDeterministicReplay(t *testing.T) {
	a, b := New(), New()
	mk := func(name string) *syncfsapi.Command {
		return &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
			Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: name}}}
	}
	for i := 0; i < 20; i++ {
		op := [16]byte{byte(i), 9}
		name := string(rune('a'+i/5)) + string(rune('0'+i%10))
		if err := a.Apply(op, uint64(i+1), mk(name)); err != nil {
			t.Fatalf("replica A apply %d: %v", i, err)
		}
		if err := b.Apply(op, uint64(i+1), mk(name)); err != nil {
			t.Fatalf("replica apply %d: %v", i, err)
		}
	}
	for _, sm := range []*StateMachine{a, b} {
		_ = sm
	}
	// Compare inode counts and op results across replicas.
	if len(a.inodes) != len(b.inodes) {
		t.Fatalf("inode count diverged: %d vs %d", len(a.inodes), len(b.inodes))
	}
	for id, x := range a.inodes {
		y, ok := b.inodes[id]
		if !ok || y.Rev != x.Rev {
			t.Fatalf("replica inode %x diverged", id)
		}
	}
	for i := 0; i < 20; i++ {
		op := [16]byte{byte(i), 9}
		ra, oka := a.GetOp(op)
		rb, okb := b.GetOp(op)
		if oka != okb || (oka && (ra.State != rb.State || ra.PayloadHash != rb.PayloadHash)) {
			t.Fatalf("op %d diverged: %+v vs %+v", i, ra, rb)
		}
	}
}
