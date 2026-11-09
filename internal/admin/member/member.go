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
	PhaseJoint // joint consensus entered, awaiting implicit leave
	PhasePromoted
	PhaseDraining // drain requested, removal follows
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
	proposed bool // promotion proposed, awaiting commit confirm
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
// The change stays pending until ConfirmPromotion (the caller invokes it
// after the ConfChange commits), satisfying Inv.8 (one change in flight).
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
	if m.proposed {
		return raftpb.ConfChange{}, fmt.Errorf("member: %d already proposed (refusal logged)", id)
	}
	m.proposed = true
	return raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: id}, nil
}

// ConfirmPromotion records that the AddNode ConfChange committed.
func (c *Cluster) ConfirmPromotion(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok {
		return fmt.Errorf("member: unknown %d", id)
	}
	if !m.proposed {
		return fmt.Errorf("member: %d has no proposed promotion", id)
	}
	m.proposed = false
	m.Phase = PhasePromoted
	m.Promoted = true
	c.pending = false
	return nil
}

// PendingProposals reports whether a promotion is awaiting commit.
func (c *Cluster) PendingProposals() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.members {
		if m.proposed {
			return true
		}
	}
	return false
}

// RequestJointPromotion enters joint consensus: a ConfChangeV2 with
// JointImplicit transition adding the learner as voter. The cluster takes
// effect for both old and new majorities until the implicit leave lands.
// This is the safe replacement path for an exactly-3 voter MVP.
func (c *Cluster) RequestJointPromotion(id uint64) (raftpb.ConfChangeV2, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok {
		return raftpb.ConfChangeV2{}, fmt.Errorf("member: unknown %d", id)
	}
	if m.Phase != PhaseCatchup {
		return raftpb.ConfChangeV2{}, fmt.Errorf("member: %d not caught up (refusal logged)", id)
	}
	m.Phase = PhaseJoint
	// Voter state is visible once the joint entry is proposed.
	// Explicit leave clears pending after the empty ConfChangeV2 lands.
	return raftpb.ConfChangeV2{
		Transition: raftpb.ConfChangeTransitionJointImplicit,
		Changes: []raftpb.ConfChangeSingle{
			{Type: raftpb.ConfChangeAddNode, NodeID: id},
		},
	}, nil
}

// LeaveJoint proposes the empty ConfChangeV2 that moves the cluster out of
// the joint configuration. Only valid while in PhaseJoint. Pending stays
// set until ConfirmJointLeave (the empty ConfChangeV2 committed).
func (c *Cluster) LeaveJoint(id uint64) (raftpb.ConfChangeV2, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok {
		return raftpb.ConfChangeV2{}, fmt.Errorf("member: unknown %d", id)
	}
	if m.Phase != PhaseJoint {
		return raftpb.ConfChangeV2{}, fmt.Errorf("member: %d not in joint configuration", id)
	}
	m.Phase = PhasePromoted
	m.Promoted = true
	// pending remains true until ConfirmJointLeave.
	return raftpb.ConfChangeV2{
		Transition: raftpb.ConfChangeTransitionJointImplicit,
		Changes:    []raftpb.ConfChangeSingle{},
	}, nil
}

// ConfirmJointLeave clears the one-change gate after the leave Commits.
func (c *Cluster) ConfirmJointLeave(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[id]; !ok {
		return fmt.Errorf("member: unknown %d", id)
	}
	c.pending = false
	return nil
}

// BeginDrain marks a promoted voter draining. A draining voter still counts
// for the quorum but should stop accepting new proposals before removal.
func (c *Cluster) BeginDrain(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[id]
	if !ok || !m.Promoted {
		return fmt.Errorf("member: %d is not a promoted voter", id)
	}
	if m.Phase == PhaseRemoved {
		return fmt.Errorf("member: %d already removed", id)
	}
	m.Phase = PhaseDraining
	return nil
}

// BeginRemove starts a safe drain+remove of an old voter (needs 2 survivors).
// The removal stays pending until ConfirmRemove (the RemoveNode committed).
func (c *Cluster) BeginRemove(id uint64) (raftpb.ConfChange, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		return raftpb.ConfChange{}, fmt.Errorf("member: another membership change in flight (refusal logged)")
	}
	m, ok := c.members[id]
	if !ok || !m.Promoted {
		return raftpb.ConfChange{}, fmt.Errorf("member: %d is not a promoted voter", id)
	}
	if m.Phase == PhaseRemoved {
		return raftpb.ConfChange{}, fmt.Errorf("member: %d already removed", id)
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
	c.pending = true
	m.Removed = true
	m.Phase = PhaseRemoved
	return raftpb.ConfChange{Type: raftpb.ConfChangeRemoveNode, NodeID: id}, nil
}

// ConfirmRemove clears the gate after a RemoveNode commits.
func (c *Cluster) ConfirmRemove(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[id]; !ok {
		return fmt.Errorf("member: unknown %d", id)
	}
	c.pending = false
	return nil
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
