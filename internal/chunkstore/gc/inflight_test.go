package gc

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
)

func TestInflightPinnedAcrossBatch(t *testing.T) {
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.Put([]byte("inflight-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCollector(1)
	c.Stage(id)
	// Mid-flight pin: batch references the chunk via InflightOp.
	reach := Reachability{InflightOp: map[chunkstore.ChunkID]bool{id: true}}
	if del := c.Sweep(reach, 1); len(del) != 0 {
		t.Fatalf("in-flight chunk must survive GC, got %v", del)
	}
	if !s.Has(id) {
		t.Fatalf("in-flight chunk deleted (race)")
	}
	// Without the pin, the same epoch deletes.
	reach2 := Reachability{}
	if del, err := c.SweepAndDelete(s, reach2, 10); err != nil {
		t.Fatal(err)
	} else if len(del) != 1 {
		t.Fatalf("unpinned chunk must delete, got %v", del)
	}
	if s.Has(id) {
		t.Fatalf("unpinned chunk must be deleted")
	}
}
