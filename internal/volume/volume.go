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
	"github.com/mohamedhabas11/syncfs/internal/volume/lease"
	"google.golang.org/protobuf/proto"
)

// Coordinator wires chunk durability to metadata commitment.
type Coordinator struct {
	mu          sync.Mutex
	fsm         *metadata.StateMachine
	stores      map[uint64]*chunkstore.Store // voter id -> chunk store
	quorum      int
	nextIdx     uint64 // monotonic RaftIndex allocator (direct-Apply path only)
	driver      DriverGapChecker // optional; consulted before serving writes
	leases      *lease.Manager // optional; revoked+waited before proposing (§8.2 step 6)
	proposeRaft func(ctx context.Context, data []byte) (uint64, error)
}

// DriverGapChecker abstracts the Raft driver's chunk-gap gate (Inv.4).
type DriverGapChecker interface {
	ChunkGap() error
}

// SetDriver wires the Raft driver for gap-gated write admission.
func (c *Coordinator) SetDriver(d DriverGapChecker) { c.driver = d }

// SetLeaseManager wires the lease manager for revocation-before-commit.
func (c *Coordinator) SetLeaseManager(m *lease.Manager) { c.leases = m }

// SetRaftProposer wires the Raft commit path (propose marshaled command,
// wait until the entry commits, return its Raft index). When set,
// ProposeCommand drives Raft commit instead of direct Apply; when nil the
// metadata-only direct-Apply path is used (tests).
func (c *Coordinator) SetRaftProposer(fn func(ctx context.Context, data []byte) (uint64, error)) {
	c.proposeRaft = fn
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
// The batch is atomic at admission: size/ctx validation happens before any
// Put, so a rejected batch leaves no orphan chunk durable.
func (c *Coordinator) WriteChunks(ctx context.Context, chunks [][]byte) ([]chunkstore.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.driver != nil {
		if err := c.driver.ChunkGap(); err != nil {
			return nil, err
		}
	}
	// Pre-validate all chunk sizes before writing any chunk (batch atomicity).
	for i, data := range chunks {
		if len(data) == 0 || len(data) > chunkstore.ChunkSize {
			return nil, fmt.Errorf("volume: chunk %d invalid size %d: %w", i, len(data), ErrNoQuorum)
		}
	}
	var receipts []chunkstore.Receipt
	for i, data := range chunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var this []chunkstore.Receipt
		for peerID, s := range c.stores {
			cid, rc, err := s.Put(data)
			if err != nil {
				continue // this voter cannot make it durable
			}
			_ = cid
			rc.PeerID = peerID
			this = append(this, rc)
		}
		if len(this) < c.quorum {
			return nil, fmt.Errorf("volume: chunk %d durable on %d/%d voters (need %d); refusing before commit: %w", i, len(this), len(c.stores), c.quorum, ErrNoQuorum)
		}
		receipts = append(receipts, this...)
	}
	return receipts, nil
}

// ProposeCommand executes a metadata-only command path: idempotent op-ID →
// apply. For WRITE_RANGES the caller MUST have attached majority
// PreparedChunks receipts (from WriteChunks) to the command; proposals
// without a quorum of distinct-peer receipts are refused (payload before
// reference, Inv.3). Raft-ordered commit lands when the state machine rides
// Raft in M2-06; caller retries never double-apply (op-ID retained).
func (c *Coordinator) ProposeCommand(ctx context.Context, opID [16]byte, cmd *syncfsapi.Command) (metadata.OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return metadata.OpResult{}, err
	}
	if c.driver != nil {
		if err := c.driver.ChunkGap(); err != nil {
			return metadata.OpResult{}, err
		}
	}
	if r, ok := c.fsm.GetOp(opID); ok {
		return *r, nil
	}
	if cmd.Kind == syncfsapi.CommandKind_WRITE_RANGES {
		rc := cmd.GetPreparedChunks()
		if len(rc) == 0 {
			return metadata.OpResult{}, fmt.Errorf("volume: WRITE_RANGES without PreparedChunks: %w", ErrNoQuorum)
		}
		peers := map[string]struct{}{}
		for _, r := range rc {
			if len(r.GetPeerId()) == 0 {
				return metadata.OpResult{}, fmt.Errorf("volume: receipt without peer: %w", ErrNoQuorum)
			}
			peers[string(r.GetPeerId())] = struct{}{}
		}
		if len(peers) < c.quorum {
			return metadata.OpResult{}, fmt.Errorf("volume: WRITE_RANGES receipts from %d distinct peers (need %d): %w", len(peers), c.quorum, ErrNoQuorum)
		}
	}
	// Revoke conflicting leases before committing (spec §8.2 step 6), then
	// wait for ack/expiry on each affected inode (bounded by lease ttl*2).
	if c.leases != nil {
		seen := map[[16]byte]bool{}
		for _, ack := range cmd.GetRequiredLeaseAcks() {
			if len(ack) == 0 {
				continue
			}
			var id uint64
			for _, b := range ack {
				id = id<<8 | uint64(b)
			}
			_ = c.leases.Revoke(id)
			if inode, ok := c.leases.InodeFor(id); ok && !seen[inode] {
				seen[inode] = true
			}
		}
		for inode := range seen {
			if err := c.leases.WaitClear(inode); err != nil {
				return metadata.OpResult{}, err
			}
		}
	}
	// Raft-ordered commit when wired: propose the marshaled command and
	// apply at the committed index (idempotency retained by op-ID).
	if c.proposeRaft != nil {
		data, err := proto.Marshal(cmd)
		if err != nil {
			return metadata.OpResult{}, err
		}
		idx, err := c.proposeRaft(ctx, data)
		if err != nil {
			return metadata.OpResult{}, err
		}
		if err := c.fsm.Apply(opID, idx, cmd); err != nil {
			return metadata.OpResult{}, err
		}
		r, _ := c.fsm.GetOp(opID)
		return *r, nil
	}
	c.nextIdx++
	idx := c.nextIdx
	if err := c.fsm.Apply(opID, idx, cmd); err != nil {
		return metadata.OpResult{}, err
	}
	r, _ := c.fsm.GetOp(opID)
	return *r, nil
}

// ErrNoQuorum is returned when writes must proceed without a majority.
var ErrNoQuorum = errors.New("volume: no quorum")
