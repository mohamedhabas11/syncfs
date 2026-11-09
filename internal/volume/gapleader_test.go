package volume

import (
	"context"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// TestGappedLeaderCannotServe proves the auto-tick window is safe: even if a
// gapped node is tick-elected leader, the coordinator refuses to serve.
func TestGappedLeaderCannotServe(t *testing.T) {
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
		// Only node 1 sees the missing chunk (others have no gap).
		present := func(h [32]byte) bool { return id != 1 || h != missing }
		d, err := driver.NewDriverWithTicks(id, voters, s, present, 15+25*int(id%4), 1)
		if err != nil {
			t.Fatal(err)
		}
		if id == 1 {
			d.SetChunkExtractor(func(raftpb.Entry) [][32]byte { return [][32]byte{missing} })
		}
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
	// Drive until node 1 (gapped) has a committed entry (seed).
	applied := make(chan raftpb.Entry, 16)
	for _, d := range drivers {
		d.SetApplyChannel(applied)
	}
	leader0 := uint64(0)
	if !until(func() bool {
		for id, d := range drivers {
			if d.LeaderID() == id {
				leader0 = id
				return true
			}
		}
		return false
	}) {
		t.Fatalf("no leader elected")
	}
	_ = drivers[leader0].Propose(ctx, []byte("seed"))
	if !until(func() bool { return drivers[1].ChunkGap() != nil }) {
		t.Fatalf("node 1 never gapped")
	}
	// Coordinator on the gapped node must refuse to serve, even though
	// tick elections still happen (vote-grant ungated per spec G-2).
	cs, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	coord := NewCoordinator(metadata.New(), map[uint64]*chunkstore.Store{1: cs})
	coord.SetDriver(drivers[1])
	if _, err := coord.WriteChunks(ctx, [][]byte{[]byte("hi")}); err == nil {
		t.Fatalf("gapped node must not serve writes (serve-gate)")
	}
}
