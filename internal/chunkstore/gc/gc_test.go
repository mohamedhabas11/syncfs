package gc

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
)

func id(n byte) chunkstore.ChunkID {
	var c chunkstore.ChunkID
	c[0] = n
	return c
}

func TestSweepEpochsAndReachability(t *testing.T) {
	c := NewCollector(1)
	// epoch 1: live + stale
	c.Stage(id(1))
	c.Stage(id(2))
	c.Advance() // gen 2
	reach := Reachability{Chunks: map[chunkstore.ChunkID]bool{id(1): true}}
	got := c.Sweep(reach, 10)
	if len(got) != 1 || got[0] != id(2) {
		t.Fatalf("stale from old epoch must sweep, got %v", got)
	}
	// lease+snapshot pinned chunks survive
	c2 := NewCollector(1)
	c2.Stage(id(3))
	c2.Advance()
	reach2 := Reachability{
		SnapshotIDs: map[chunkstore.ChunkID]bool{id(3): true},
		LeasedIDs:   map[chunkstore.ChunkID]bool{id(3): true},
	}
	if got := c2.Sweep(reach2, 10); len(got) != 0 {
		t.Fatalf("pinned chunks must survive: %v", got)
	}
	// recent epochs are never swept
	c3 := NewCollector(5)
	c3.Stage(id(4))
	if got := c3.Sweep(Reachability{}, 1); len(got) != 0 {
		t.Fatalf("current epoch must never sweep: %v", got)
	}
}
