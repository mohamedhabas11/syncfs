package volume

import (
	"context"
	"testing"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// TestVolumeConsultsRealDriverChunkGap wires a real raft mesh into the
// coordinator and verifies gated writes are refused while gapped.
func TestVolumeConsultsRealDriverChunkGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	voters := []uint64{1, 2, 3}
	stores := map[uint64]*wal.Storage{}
	drivers := map[uint64]*driver.Driver{}
	missing := [32]byte{}
	for i := range missing {
		missing[i] = 0xAA
	}
	for _, id := range voters {
		s, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
		if err != nil {
			t.Fatal(err)
		}
		stores[id] = s
		d, err := driver.NewDriverWithTicks(id, voters, s, func(h [32]byte) bool { return h != missing }, 15+25*int(id%4), 1)
		if err != nil {
			t.Fatal(err)
		}
		d.SetChunkExtractor(func(raftpb.Entry) [][32]byte { return [][32]byte{missing} })
		drivers[id] = d
	}
	defer func() {
		for _, d := range drivers {
			_ = d.Stop()
		}
	}()
	round := func(n int) {
		if d, ok := drivers[voters[n%len(voters)]]; ok {
			d.Tick()
		}
		for {
			quiet := true
			for _, d := range drivers {
				msgs, err := d.Step(ctx)
				if err != nil {
					return
				}
				if len(msgs) > 0 {
					quiet = false
				}
				for _, m := range msgs {
					if peer, ok := drivers[m.To]; ok {
						_ = peer.StepMsg(ctx, m)
					}
				}
			}
			if quiet {
				return
			}
		}
	}
	roundN := 0
	until := func(cond func() bool) bool {
		for {
			select {
			case <-ctx.Done():
				return false
			default:
			}
			if cond() {
				return true
			}
			round(roundN)
			roundN++
			time.Sleep(2 * time.Millisecond)
		}
	}
	leader := uint64(0)
	if !until(func() bool {
		for id, d := range drivers {
			if d.LeaderID() == id {
				leader = id
				return true
			}
		}
		return false
	}) {
		t.Fatalf("no leader elected")
	}
	// Commit a raw entry so ChunkGap becomes non-nil (log is non-empty).
	applied := make(chan raftpb.Entry, 16)
	for _, d := range drivers {
		d.SetApplyChannel(applied)
	}
	if err := drivers[leader].Propose(ctx, []byte("seed")); err != nil {
		t.Fatal(err)
	}
	if !until(func() bool { return drivers[leader].ChunkGap() != nil }) {
		t.Fatalf("gap never appeared after seed commit")
	}
	// Extracted manifests prove gating; force a manifest-bearing entry too.
	man := make([]byte, 32)
	copy(man, missing[:])
	cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_WRITE_RANGES, Payload: &syncfsapi.Command_WriteRanges{
		WriteRanges: &syncfsapi.WriteRangesPayload{NewManifest: man}}}
	data, _ := proto.Marshal(cmd)
	_ = data // manifest proof is via extractor (all entries flagged); raw seed suffices
	fsm := metadata.New()
	newStore := func() *chunkstore.Store {
		s, err := chunkstore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	c := NewCoordinator(fsm, map[uint64]*chunkstore.Store{1: newStore()})
	c.SetDriver(drivers[leader])
	if _, err := c.WriteChunks(ctx, [][]byte{[]byte("hi")}); err == nil {
		t.Fatalf("gated coordinator must refuse writes while ChunkGap present")
	}
}
