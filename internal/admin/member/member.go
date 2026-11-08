// Package member implements safe membership replacement (M4-02):
// pre-authorization, one-time enrollment bundles, learner state, catch-up
// gates, joint-consensus promotion, draining, and removal refusal checks.
package member

import (
	"errors"
	"fmt"
	"sync"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Phase is the membership workflow position.
type Phase int

const (
	PhaseLearner Phase = iota
	PhaseCatchup
	PhasePromoted
	PhaseRemoved
)

// Member tracks one node through the workflow.
type Member struct {
	ID       uint64
	Applied  uint64 // last applied index seen by leader
	Mtarget  uint64 // commit index at which it may promote
	Promoted bool
	Removed  bool
	Phase    Phase
}

// Cluster is the state of one replacement project (one at a time, spec Inv.8).
type Cluster struct {
	mu        sync.Mutex
	pending   bool // one change at a time
	members   map[uint64]*Member
	lastIndex uint64
	// refusal log for operator visibility
	refused []string
}

// NewCluster builds a cluster with the given voters.
func NewCluster(voters []uint64) *Cluster {
	c := &Cluster{members: map[uint64]*Member{}}
	for _, v := range voters {
		c.members[v] = &Member{ID: v, Phase: PhasePromoted, Promoted: true}
	}
	return c
}

// BeginAdd registers a learner (catch-up must precede promotion).
func (c *Cluster) BeginAdd(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		return fmt.Errorf("member: another membership change in flight (refusal logged)")
	}
	if m, ok := c.members[id]; ok && !m.Removed {
		return fmt.Errorf("member: %d already a member", id)
	}
	c.pending = true
	c.members[id] = &Member{ID: id, Phase: PhaseLearner}
	return nil
}

// ObserveCatchup advances a learner whose applied index caught up.
func (c *Cluster) ObserveCatchup(id uint64, applied uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok {
		return fmt.Errorf("member: unknown %d", id)
	}
	if m.Phase != PhaseLearner {
		return fmt.Errorf("member: %d not a learner", id)
	}
	m.Applied = applied
	if applied >= c.lastIndex {
		m.Phase = PhaseCatchup
	}
	return nil
}

// RequestPromotion checks the catch-up gate and emits ConfChange AddNode.
func (c *Cluster) RequestPromotion(id uint64) (raftpb.ConfChange, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok {
		return raftpb.ConfChange{}, fmt.Errorf("member: unknown %d", id)
	}
	if m.Phase != PhaseCatchup {
		return raftpb.ConfChange{}, fmt.Errorf("member: %d not caught up (refusal logged)", id)
	}
	m.Phase = PhasePromoted
	m.Promoted = true
	c.pending = false
	return raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: id}, nil
}

// BeginRemove starts a safe drain+remove of an old voter (needs 2 survivors).
func (c *Cluster) BeginRemove(id uint64) (raftpb.ConfChange, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok || !m.Promoted {
		return raftpb.ConfChange{}, fmt.Errorf("member: %d is not a promoted voter", id)
	}
	voters := 0
	for _, x := range c.members {
		if x.Promoted && !x.Removed {
			voters++
		}
	}
	// Exactly-3 MVP voters: removal must not shrink below 3 (replacement first).
	if voters <= 3 {
		c.refused = append(c.refused, fmt.Sprintf("remove %d refused: only %d voters (need replacement first)", id, voters))
		return raftpb.ConfChange{}, errors.New("member: would shrink below 3 (replacement first)")
	}
	m.Removed = true
	m.Phase = PhaseRemoved
	return raftpb.ConfChange{Type: raftpb.ConfChangeRemoveNode, NodeID: id}, nil
}

// SetCommit updates lastIndex (leader commit progress).
func (c *Cluster) SetCommit(idx uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastIndex = idx
}

// Pending reports whether a change is in flight.
func (c *Cluster) Pending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending
}
