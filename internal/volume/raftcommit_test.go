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
)

// TestProposeViaRaftCommit wires the Raft committer: a command proposed via
// the mesh commits and is then applied by the coordinator at the Raft index.
func TestProposeViaRaftCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	voters := []uint64{1, 2, 3}
	stores := map[uint64]*wal.Storage{}
	drivers := map[uint64]*driver.Driver{}
	for _, id := range voters {
		s, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
		if err != nil {
			t.Fatal(err)
		}
		stores[id] = s
		d, err := driver.NewDriverWithTicks(id, voters, s, func([32]byte) bool { return true }, 15+25*int(id%4), 1)
		if err != nil {
			t.Fatal(err)
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
	applied := make(chan raftpb.Entry, 32)
	for _, d := range drivers {
		d.SetApplyChannel(applied)
	}
	fsm := metadata.New()
	cs, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	coord := NewCoordinator(fsm, map[uint64]*chunkstore.Store{1: cs})
	coord.SetDriver(drivers[leader])
	var committedIdx uint64
	coord.SetRaftProposer(func(ctx context.Context, data []byte) (uint64, error) {
		if err := drivers[leader].Propose(ctx, data); err != nil {
			return 0, err
		}
		// Drive until our marshaled entry commits (seen on the channel).
		for {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
			round(roundN)
			roundN++
			for {
				select {
				case e := <-applied:
					if len(e.Data) > 0 && e.Data[0] == data[0] {
						// Match by index ordering: first non-empty committed entry
						// after our propose is ours (single in-flight test).
						committedIdx = e.Index
						return e.Index, nil
					}
				default:
					goto next
				}
			}
		next:
			time.Sleep(2 * time.Millisecond)
		}
	})
	cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
		Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "rafty"}}}
	r, err := coord.ProposeCommand(ctx, [16]byte{1}, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if r.RaftIndex == 0 || r.RaftIndex != committedIdx {
		t.Fatalf("RaftIndex %d want committed %d", r.RaftIndex, committedIdx)
	}
	if _, ok := fsm.Lookup([16]byte{}, "rafty"); !ok {
		t.Fatalf("command not applied after Raft commit")
	}
}
