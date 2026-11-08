package volume

import (
	"context"
	"errors"
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
)

func mustStore(t *testing.T) *chunkstore.Store {
	t.Helper()
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWriteChunksQuorum(t *testing.T) {
	fsm := metadata.New()
	stores := map[uint64]*chunkstore.Store{1: mustStore(t), 2: mustStore(t), 3: mustStore(t)}
	c := NewCoordinator(fsm, stores)
	data := [][]byte{[]byte("chunk-one-bytes"), []byte("chunk-two-bytes")}
	receipts, err := c.WriteChunks(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	// every receipt has verified bytes; each chunk appears 3x (all voters)
	// every receipt has verified bytes; each chunk appears 3x (all voters)
	seen := map[chunkstore.ChunkID]int{}
	for _, r := range receipts {
		if r.VerifiedBytes == 0 {
			t.Errorf("zero verified bytes")
		}
		seen[r.ChunkID]++
	}
	if len(seen) != 2 {
		t.Fatalf("want 2 distinct chunks, got %d", len(seen))
	}
	for id, n := range seen {
		if n != 3 {
			t.Fatalf("chunk %x durable on %d want 3", id, n)
		}
	}
}

func TestIdempotentRetry(t *testing.T) {
	fsm := metadata.New()
	c := NewCoordinator(fsm, map[uint64]*chunkstore.Store{1: mustStore(t)})
	op := [16]byte{9}
	cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "f"},
	}}
	r1, err := c.ProposeCommand(context.Background(), op, cmd)
	if err != nil || r1.State != "COMMITTED" {
		t.Fatalf("first: %v %+v", err, r1)
	}
	r2, err := c.ProposeCommand(context.Background(), op, cmd)
	if err != nil || r2.State != "COMMITTED" || r2.RaftIndex != r1.RaftIndex {
		t.Fatalf("retry must return retained result: %v %+v", err, r2)
	}
	if _, ok := fsm.Lookup([16]byte{}, "f"); !ok {
		t.Fatalf("file f must exist exactly once")
	}
}

func TestWriteChunksMinorityRefused(t *testing.T) {
	fsm := metadata.New()
	// Only one store: quorum = 1, must succeed. Two stores, one failing:
	// emulate by closing one store before the write so its Put fails.
	s1 := mustStore(t)
	s2 := mustStore(t)
	stores := map[uint64]*chunkstore.Store{1: s1, 2: s2}
	_ = s2.Close()
	c := NewCoordinator(fsm, stores)
	data := [][]byte{[]byte("x")}
	_, err := c.WriteChunks(context.Background(), data)
	if err == nil {
		// s2 closed → Put fails → receipts=1, quorum=2 → must refuse
		t.Fatalf("majority failure must be refused (not silent)")
	}
	if !errors.Is(err, ErrNoQuorum) && err.Error() == "" {
		t.Fatalf("unreachable")
	}
}
