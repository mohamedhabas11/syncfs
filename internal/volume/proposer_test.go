package volume

import (
	"context"
	"errors"
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
)

func TestWriteRangesWithoutProposerRefused(t *testing.T) {
	fsm := metadata.New()
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := NewCoordinator(fsm, map[uint64]*chunkstore.Store{1: s})
	man := make([]byte, 32)
	cmd := &syncfsapi.Command{
		Kind:    syncfsapi.CommandKind_WRITE_RANGES,
		Payload: &syncfsapi.Command_WriteRanges{WriteRanges: &syncfsapi.WriteRangesPayload{NewManifest: man}},
		PreparedChunks: []*syncfsapi.PreparedChunkReceipt{
			{ChunkId: man, PeerId: []byte{1}},
		},
	}
	// Even with 1/1 receipts (quorum met), no proposer → direct data path refused.
	if _, err := c.ProposeCommand(context.Background(), [16]byte{1}, cmd); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("WRITE_RANGES without proposer must fail ErrNoQuorum, got %v", err)
	}
	// Metadata-only CREATE still direct-applies (no proposer needed).
	cre := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "f"}}}
	if _, err := c.ProposeCommand(context.Background(), [16]byte{2}, cre); err != nil {
		t.Fatalf("CREATE without proposer must succeed: %v", err)
	}
}
