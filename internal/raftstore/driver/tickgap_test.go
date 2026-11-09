package driver

import (
	"context"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// TestGappedTickDoesNotElect seeds a gapped committed entry in node 1's WAL
// before any ticks, so ChunkGap is true from the first tick. Gated ticks
// must never let node 1 campaign to leader.
func TestGappedTickDoesNotElect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	voters := []uint64{1, 2, 3}
	stores := map[uint64]*wal.Storage{}
	drivers := map[uint64]*Driver{}
	missing := [32]byte{}
	for i := range missing {
		missing[i] = 0xAA
	}
	// Seed node 1's WAL with a committed entry (gap from the start).
	s1, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SaveReady(raft.Ready{
		Entries:   []raftpb.Entry{{Term: 1, Index: 1, Data: []byte("seed")}},
		HardState: raftpb.HardState{Term: 1, Commit: 1},
	}); err != nil {
		t.Fatal(err)
	}
	stores[1] = s1
	for _, id := range voters[1:] {
		s, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
		if err != nil {
			t.Fatal(err)
		}
		stores[id] = s
	}
	for _, id := range voters {
		d, err := NewDriverWithTicks(id, voters, stores[id], func(h [32]byte) bool { return id != 1 || h != missing }, 15+25*int(id%4), 1)
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
	// Sanity: node 1 starts gapped.
	if err := drivers[1].ChunkGap(); err == nil {
		t.Fatalf("node 1 must start gapped")
	}
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
	for i := 0; i < 300; i++ {
		round(i)
		if drivers[1].LeaderID() == 1 {
			t.Fatalf("gapped node 1 tick-elected leader (Tick gating broken)")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
