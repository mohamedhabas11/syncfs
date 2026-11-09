package volume

import (
	"context"
	"testing"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/volume/lease"
)

func TestProposeRevokesLeases(t *testing.T) {
	fsm := metadata.New()
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := NewCoordinator(fsm, map[uint64]*chunkstore.Store{1: s})
	mgr := lease.NewManager(time.Minute)
	c.SetLeaseManager(mgr)
	l := mgr.Grant([16]byte{1}, 1)
	if l == nil {
		t.Fatal("grant must succeed")
	}
	// Encode lease ID as 8-byte big-endian (caller convention).
	idBytes := make([]byte, 8)
	v := l.ID
	for i := 7; i >= 0; i-- {
		idBytes[i] = byte(v)
		v >>= 8
	}
	cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "f"}}}
	cmd.RequiredLeaseAcks = [][]byte{idBytes}
	if _, err := c.ProposeCommand(context.Background(), [16]byte{1}, cmd); err != nil {
		t.Fatal(err)
	}
	if mgr.Valid(l) {
		t.Fatalf("lease must be revoked before commit")
	}
}
