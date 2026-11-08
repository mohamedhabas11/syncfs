package driver

import (
	"context"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// mesh is the in-memory test transport (mirrors testkit/cluster).
type mesh struct {
	drivers map[uint64]*Driver
	order   []uint64
}

func newMesh(t *testing.T, n int) (*mesh, map[uint64]*wal.Storage) {
	t.Helper()
	voters := make([]uint64, n)
	for i := range voters {
		voters[i] = uint64(i + 1)
	}
	stores := map[uint64]*wal.Storage{}
	drivers := map[uint64]*Driver{}
	for _, id := range voters {
		s, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
		if err != nil {
			t.Fatal(err)
		}
		stores[id] = s
		d, err := NewDriverWithTicks(id, voters, s, func([32]byte) bool { return true },
			15+25*int(id%4), 1)
		if err != nil {
			t.Fatal(err)
		}
		drivers[id] = d
	}
	return &mesh{drivers: drivers, order: voters}, stores
}

func (m *mesh) round(ctx context.Context, n int) error {
	// Single-node tick per round (like etcd's interaction harness): concurrent
	// ticks let every node self-vote and re-lock the election forever.
	nt := m.drivers[m.order[n%len(m.order)]]
	nt.Tick()
	// Deliver every pending Ready from every node, looping until quiet.
	for {
		quiet := true
		for _, id := range m.order {
			d := m.drivers[id]
			msgs, err := d.Step(ctx)
			if err != nil {
				return err
			}
			if len(msgs) > 0 {
				quiet = false
			}
			for _, msg := range msgs {
				if peer, ok := m.drivers[msg.To]; ok {
					_ = peer.StepMsg(ctx, msg)
				}
			}
		}
		if quiet {
			break
		}
	}
	return nil
}

func (m *mesh) leaderID() uint64 {
	for id, d := range m.drivers {
		if d.LeaderID() == id {
			return id
		}
	}
	return 0
}

func (m *mesh) until(ctx context.Context, timeout time.Duration, cond func(*mesh) bool) bool {
	deadline := time.After(timeout)
	roundN := 0
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		default:
		}
		if cond(m) {
			return true
		}
		if err := m.round(ctx, roundN); err != nil {
			return false
		}
		roundN++
		// Pace the loop: a hot loop ticks faster than messages round-trip,
		// which re-locks the split-vote. 2ms approximates LAN RPC latency.
		time.Sleep(2 * time.Millisecond)
	}
}

func TestThreeNodeElectAndCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, stores := newMesh(t, 3)
	for _, s := range stores {
		defer s.Close()
	}
	for _, d := range m.drivers {
		defer d.Stop()
	}

	if !m.until(ctx, 10*time.Second, func(m *mesh) bool { return m.leaderID() != 0 }) {
		t.Fatalf("no leader elected")
	}
	// Propose a write on the leader; it must commit on at least 2/3 nodes.
	lid := m.leaderID()
	if err := m.drivers[lid].Propose(ctx, []byte("hello-raft")); err != nil {
		t.Fatal(err)
	}
	committed := make(chan raftpb.Entry, 8)
	for _, d := range m.drivers {
		d.SetApplyChannel(committed)
	}
	if !m.until(ctx, 10*time.Second, func(m *mesh) bool {
		for {
			select {
			case e := <-committed:
				if string(e.Data) == "hello-raft" {
					return true
				}
			default:
				return false
			}
		}
	}) {
		t.Fatalf("entry did not commit")
	}
}
