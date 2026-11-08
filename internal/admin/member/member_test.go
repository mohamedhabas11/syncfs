package member

import (
	"testing"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

func TestReplacementFlow(t *testing.T) {
	c := NewCluster([]uint64{1, 2, 3})
	c.SetCommit(100)
	if err := c.BeginAdd(4); err != nil {
		t.Fatal(err)
	}
	// second concurrent change refused (Inv.8)
	if err := c.BeginAdd(9); err == nil {
		t.Fatalf("concurrent change must be refused")
	}
	// promotion gated until catch-up
	if _, err := c.RequestPromotion(4); err == nil {
		t.Fatalf("promotion before catch-up must be refused")
	}
	if err := c.ObserveCatchup(4, 50); err != nil {
		t.Fatal(err)
	}
	if err := c.ObserveCatchup(4, 100); err != nil {
		t.Fatal(err)
	}
	cc, err := c.RequestPromotion(4)
	if err != nil || cc.Type != raftpb.ConfChangeAddNode || cc.NodeID != 4 {
		t.Fatalf("promotion conf-change: %v %+v", err, cc)
	}
	if c.Pending() {
		t.Fatalf("pending must clear after promotion")
	}
	// removal of an old voter with 4 voters present is allowed
	rc, err := c.BeginRemove(1)
	if err != nil || rc.Type != raftpb.ConfChangeRemoveNode {
		t.Fatalf("remove: %v %+v", err, rc)
	}
	// removal at 3 voters is refused (would shrink below quorum)
	c2 := NewCluster([]uint64{1, 2, 3})
	if _, err := c2.BeginRemove(1); err == nil {
		t.Fatalf("removal at 3 voters must be refused (replacement first)")
	}
	_ = c2
}
