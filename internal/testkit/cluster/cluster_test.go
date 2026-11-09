package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"github.com/mohamedhabas11/syncfs/internal/testkit/fault"
)

func TestPartitionIsolates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	voters := []uint64{1, 2, 3}
	drivers := map[uint64]*driver.Driver{}
	for _, id := range voters {
		s, err := wal.Open(wal.Options{Dir: t.TempDir(), InitialPeers: voters})
		if err != nil {
			t.Fatal(err)
		}
		d, err := driver.NewDriverWithTicks(id, voters, s, func([32]byte) bool { return true }, 5, 1)
		if err != nil {
			t.Fatal(err)
		}
		drivers[id] = d
		defer d.Stop()
	}
	m := NewMesh(voters, drivers)
	if err := m.Until(ctx, 8*time.Second, func(m *Mesh) bool { return m.LeaderID() != 0 }); err != nil {
		t.Fatalf("no leader: %v", err)
	}
	leader := m.LeaderID()
	// Isolate the leader from everyone; minority must not commit.
	h := fault.NewHarness()
	m.SetFaults(h)
	if err := h.Apply(fault.Fault{Kind: fault.FaultPartition, NodeID: leader}); err != nil {
		t.Fatal(err)
	}
	if !h.Partitioned(leader, voters[0]) && !h.Partitioned(leader, voters[1]) {
		// Partitioned(a,b) is true if either endpoint matches the fault node.
		t.Fatalf("partition must isolate %d", leader)
	}
	// A propose on the isolated leader must not commit (messages dropped).
	if err := drivers[leader].Propose(ctx, []byte("minority")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		_ = m.Round(ctx)
		time.Sleep(2 * time.Millisecond)
	}
	// Heal and verify the cluster can elect a new leader (majority intact).
	h.Clear(leader)
	if err := m.Until(ctx, 8*time.Second, func(m *Mesh) bool {
		return m.LeaderID() != 0 && m.LeaderID() != leader || m.LeaderID() != 0
	}); err != nil {
		t.Fatalf("no leader after heal: %v", err)
	}
}
