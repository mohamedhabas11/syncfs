package gc

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
)

func TestComposeAndSweepPins(t *testing.T) {
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	keep, _, _ := s.Put([]byte("keep-by-manifest"))
	drop, _, _ := s.Put([]byte("drop-unreferenced"))
	c := NewCollector(1)
	c.Stage(keep)
	c.Stage(drop)
	// Keep pin survives; unreferenced past epoch deletes.
	del, err := c.ComposeAndSweep(s, []chunkstore.ChunkID{keep}, nil, nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(del) != 1 || del[0] != drop {
		t.Fatalf("only unpinned should delete, got %v", del)
	}
	if !s.Has(keep) {
		t.Fatalf("pinned chunk deleted")
	}
	if s.Has(drop) {
		t.Fatalf("unpinned chunk must be deleted")
	}
}
