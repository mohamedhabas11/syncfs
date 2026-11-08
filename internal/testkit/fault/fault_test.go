package fault

import (
	"testing"
	"time"
)

func TestApplyAndClear(t *testing.T) {
	h := NewHarness()
	_ = h.Apply(Fault{Kind: FaultKill, NodeID: 3, Duration: time.Second})
	if h.Partitioned(3, 1) {
		t.Fatalf("kill alone is not a partition; only FaultPartition cuts links")
	}
	_ = h.Apply(Fault{Kind: FaultPartition, NodeID: 2, Duration: 5 * time.Second})
	if !h.Partitioned(2, 1) {
		t.Fatalf("node 2 must be partitioned")
	}
	if h.Partitioned(1, 3) {
		t.Fatalf("node 3 must stay reachable from 1")
	}
	if len(h.History()) != 2 {
		t.Fatalf("history must hold both, got %d", len(h.History()))
	}
	h.Clear(2)
	if len(h.Active()) != 1 || h.Partitioned(2, 1) {
		t.Fatalf("clear must drop only node-2 faults, got %v", h.Active())
	}
}

func TestIsRealizable(t *testing.T) {
	h := NewHarness()
	ok, err := h.IsRealizable(Fault{Kind: FaultDelay, NodeID: 1})
	if err != nil || !ok {
		t.Fatalf("delay must be realizable")
	}
	if ok, err := h.IsRealizable(Fault{Kind: FaultKind(99), NodeID: 1}); ok || err == nil {
		t.Fatalf("unknown kind must fail")
	}
}
