// Package volume implements the quorum write coordinator (M2-05):
// majority chunk preparation, preconditioned command proposal, and
// idempotent retries (payload before reference, spec Inv.3).
package volume

import (
	"context"
	"errors"
	"fmt"
	"sync"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
)

// Coordinator wires chunk durability to metadata commitment.
type Coordinator struct {
	mu     sync.Mutex
	fsm    *metadata.StateMachine
	stores map[uint64]*chunkstore.Store // voter id -> chunk store
	quorum int
}

// NewCoordinator: stores keyed by voter id. quorum = majority of voters.
func NewCoordinator(fsm *metadata.StateMachine, stores map[uint64]*chunkstore.Store) *Coordinator {
	return &Coordinator{
		fsm:    fsm,
		stores: stores,
		quorum: len(stores)/2 + 1,
	}
}

// WriteChunks stages chunks locally (fsync) then fans out to a majority.
// Returns durable receipts from at least `quorum` voters, or an error if a
// majority cannot be made durable (e.g. disk full: refuse before commit).
func (c *Coordinator) WriteChunks(ctx context.Context, chunks [][]byte) ([]chunkstore.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var receipts []chunkstore.Receipt
	for _, data := range chunks {
		var this []chunkstore.Receipt
		for _, s := range c.stores {
			cid, rc, err := s.Put(data)
			if err != nil {
				continue // this voter cannot make it durable
			}
			_ = cid
			this = append(this, rc)
		}
		if len(this) < c.quorum {
			return nil, fmt.Errorf("volume: chunk %d durable on %d/%d voters (need %d); refusing before commit", 0, len(this), len(c.stores), c.quorum)
		}
		receipts = append(receipts, this...)
	}
	return receipts, nil
}

// ProposeCommand executes a metadata-only command path: idempotent op-ID →
// apply. Raft-ordered proposal lands when the state machine rides Raft in
// M2-06; caller retries never double-apply (op-ID retained).
func (c *Coordinator) ProposeCommand(ctx context.Context, opID [16]byte, cmd *syncfsapi.Command) (metadata.OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.fsm.GetOp(opID); ok {
		return *r, nil
	}
	idx := uint64(1)
	if err := c.fsm.Apply(opID, idx, cmd); err != nil {
		return metadata.OpResult{}, err
	}
	r, _ := c.fsm.GetOp(opID)
	return *r, nil
}

// ErrNoQuorum is returned when writes must proceed without a majority.
var ErrNoQuorum = errors.New("volume: no quorum")
