// Package driver wires raft to the metadata state machine (M2-04):
// Ready loop with persistence-before-send, leader routing, ReadIndex,
// committed-entry apply barrier, and chunk-gap campaign gating (Inv.4).
//
// Chunk-gap contract (§8.4, Inv.4): a replica MUST NOT apply a committed
// command referencing an absent hash-invalid chunk, and MUST NOT campaign
// for leader while such an entry remains unapplied. Gating is active only
// when the caller wires `chunkPresent` AND committed entries carry
// manifests (default extractor decodes Command WriteRanges NewManifest +
// PreparedChunk receipts; raw test entries carry no manifest → no gap).
// Vote-grant is NOT gated (promoted-but-unapplied voters still vote, G-2).
package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
)

// ErrChunkGap is returned when a committed entry references a chunk that is
// not locally durable (caller must fetch+verify before apply/campaign).
var ErrChunkGap = errors.New("driver: chunk gap")

// Driver owns the etcd/raft.Node for one replica.
type Driver struct {
	node         raft.Node
	storage      *wal.Storage
	config       raft.Config
	chunkPresent func(hash [32]byte) bool
	// mu serializes all raft Node calls (Tick/Step/StepMsg/Propose/
	// Campaign/ApplyConfChange). etcd's Node is not thread-safe; daemons
	// call Tick/Step from the tick loop and StepMsg from QUIC accept
	// goroutines concurrently.
	mu sync.Mutex

	// chunkExtractor maps a committed entry to referenced chunk hashes.
	// Nil means the default Command decoder (WriteRanges manifests).
	chunkExtractor func(raftpb.Entry) [][32]byte

	applyRequested chan<- raftpb.Entry
	// pendingApply defers gap-blocked entries (and channel-full backpressure)
	// without blocking Ready processing (Inv.4 + §8.4 rule 4).
	applyMu sync.Mutex
	pending []raftpb.Entry
}

// NewDriver builds a driver for the given id/voters.
func NewDriver(id uint64, voters []uint64, storage *wal.Storage, chunkPresent func(hash [32]byte) bool) (*Driver, error) {
	return NewDriverWithTicks(id, voters, storage, chunkPresent, 10, 1)
}

// NewDriverWithTicks allows jittered ticks (production etcd does the same).
func NewDriverWithTicks(id uint64, voters []uint64, storage *wal.Storage, chunkPresent func(hash [32]byte) bool, electionTick, heartbeatTick int) (*Driver, error) {
	if storage == nil {
		return nil, errors.New("driver: storage is required")
	}
	if len(voters) == 0 {
		return nil, errors.New("driver: voters are required (InitialPeers alone is not enough)")
	}
	found := false
	for _, v := range voters {
		if v == id {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("driver: id %d not in voters", id)
	}
	cfg := raft.Config{
		ID:              id,
		ElectionTick:    electionTick,
		HeartbeatTick:   heartbeatTick,
		Storage:         storage,
		MaxSizePerMsg:   64 << 10,
		MaxInflightMsgs: 100,
		CheckQuorum:     true,
		PreVote:         false, // ADR-0004: disabled for VM test fleet (see docs/adr/0004-prevote-test-fleet.md); re-enable per spec 8.4 for production
		// Bounded uncommitted entries (spec §8.4): fail closed on runaway
		// proposal buffering instead of unbounded memory growth.
		MaxUncommittedEntriesSize: 1 << 20, // 1 MiB
	}
	n := raft.RestartNode(&cfg)
	return &Driver{
		node:         n,
		storage:      storage,
		config:       cfg,
		chunkPresent: chunkPresent,
	}, nil
}

// SetApplyChannel registers where committed entries are delivered (M2-05).
func (d *Driver) SetApplyChannel(ch chan<- raftpb.Entry) { d.applyRequested = ch }

// SetChunkExtractor overrides the manifest decoder (tests / non-Command data).
func (d *Driver) SetChunkExtractor(f func(raftpb.Entry) [][32]byte) { d.chunkExtractor = f }

// ReadyCh exposes the underlying raft Ready channel. Consumers MUST drain it
// from a dedicated loop (the daemon tick loop now does); etcd's Node is not
// thread-safe and Tick/Step may race with a blocking Ready reader.
func (d *Driver) ReadyCh() <-chan raft.Ready { return d.node.Ready() }

// Tick advances the logical clock. A gapped node (missing committed chunks)
// does not tick: it must not campaign (Inv.4). Vote grants still arrive via
// StepMsg (ungated, G-2) so a promoted-but-unapplied voter can still vote.
func (d *Driver) Tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ChunkGap() != nil {
		return
	}
	d.node.Tick()
}

// Step processes one Ready cycle if any is pending: persist, then expose
// messages for sending. Persistence happens before Ready.Messages are
// returned (spec 8.4 rule 1). Non-blocking: (nil, nil) when nothing pending.
//
// Apply delivery never blocks Ready (spec 8.4 rule 4): committed entries
// that reference missing chunks (or find a full channel) are deferred to
// the pending queue; call RetryApply after fetching the chunks.
func (d *Driver) Step(ctx context.Context) ([]raftpb.Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	select {
	case r := <-d.node.Ready():
		return d.stepReady(r)
	default:
		return nil, nil
	}
}

// StepReady processes one explicitly-delivered Ready cycle (dedicated
// consumer loop; see Driver.ReadyCh).
func (d *Driver) StepReady(r raft.Ready) ([]raftpb.Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stepReady(r)
}

// stepReady assumes d.mu is held (called from Step / StepReady under the lock).
func (d *Driver) stepReady(r raft.Ready) ([]raftpb.Message, error) {
	if err := d.storage.SaveReady(r); err != nil {
		// Always advance the Raft state machine even when persistence fails:
		// returning before Advance() wedges the node permanently (2026-11-10
		// execution review, Iter-8-03).
		d.node.Advance()
		return nil, fmt.Errorf("driver: save ready: %w", err)
	}
	d.node.Advance()
	// Retry earlier deferred entries first (preserves in-order apply).
	d.retryPendingLocked()
	for _, e := range r.CommittedEntries {
		if d.entryGap(e) != nil {
			d.applyMu.Lock()
			d.pending = append(d.pending, e)
			d.applyMu.Unlock()
			continue
		}
		d.deliver(e)
	}
	return r.Messages, nil
}

// deliver sends e to the apply channel without blocking; overflow defers.
func (d *Driver) deliver(e raftpb.Entry) {
	if d.applyRequested == nil {
		return
	}
	select {
	case d.applyRequested <- e:
	default:
		d.applyMu.Lock()
		d.pending = append(d.pending, e)
		d.applyMu.Unlock()
	}
}

// RetryApply delivers deferred entries whose chunks are now present.
// Returns the number delivered. Call after fetching missing chunks.
func (d *Driver) RetryApply() int {
	d.retryPendingLocked()
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	return len(d.pending)
}

// PendingApply reports deferred (not yet applied) committed entries.
func (d *Driver) PendingApply() int {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	return len(d.pending)
}

func (d *Driver) retryPendingLocked() {
	if d.applyRequested == nil {
		return
	}
	// Drain in order; stop at the first still-gapped entry (preserves order).
	d.applyMu.Lock()
	var rest []raftpb.Entry
	failedOrStamp := false
	for _, e := range d.pending {
		if failedOrStamp || d.entryGap(e) != nil {
			rest = append(rest, e)
			failedOrStamp = true
			continue
		}
		select {
		case d.applyRequested <- e:
		default:
			rest = append(rest, e)
			failedOrStamp = true
		}
	}
	d.pending = rest
	d.applyMu.Unlock()
}

// entryGap returns a wrapped ErrChunkGap if e references a missing chunk.
func (d *Driver) entryGap(e raftpb.Entry) error {
	if d.chunkPresent == nil {
		return nil
	}
	for _, h := range d.extract(e) {
		if !d.chunkPresent(h) {
			return fmt.Errorf("%w: entry %d references missing chunk %x", ErrChunkGap, e.Index, h[:8])
		}
	}
	return nil
}

// extract returns referenced chunk hashes for e (default: Command manifests).
func (d *Driver) extract(e raftpb.Entry) [][32]byte {
	if d.chunkExtractor != nil {
		return d.chunkExtractor(e)
	}
	var cmd syncfsapi.Command
	if err := proto.Unmarshal(e.Data, &cmd); err != nil {
		return nil // opaque/raw entry: no manifest → no gap
	}
	if cmd.Kind != syncfsapi.CommandKind_WRITE_RANGES {
		return nil
	}
	var out [][32]byte
	if m := cmd.GetWriteRanges().GetNewManifest(); len(m) == 32 {
		var h [32]byte
		copy(h[:], m)
		out = append(out, h)
	}
	for _, r := range cmd.GetPreparedChunks() {
		if id := r.GetChunkId(); len(id) == 32 {
			var h [32]byte
			copy(h[:], id)
			out = append(out, h)
		}
	}
	return out
}

// ChunkGap reports whether any committed entry up to the committed index
// references a missing chunk (Inv.4). Nil chunkPresent means no data → nil.
func (d *Driver) ChunkGap() error {
	if d.chunkPresent == nil {
		return nil
	}
	hs, _, err := d.storage.InitialState()
	if err != nil {
		return err
	}
	// Scan the whole local log (committed + uncommitted): a node holding a
	// gap entry in its log must not campaign, whether or not the entry has
	// committed yet (commit resets to 0 on restart until re-committed).
	_ = hs
	commit := hs.Commit
	last, lerr := d.storage.LastIndex()
	if lerr != nil {
		return lerr
	}
	if last == 0 && commit == 0 {
		return nil
	}
	end := last
	if end == 0 {
		end = commit
	}
	first, err := d.storage.FirstIndex()
	if err != nil {
		return err
	}
	lo := first
	if lo > end+1 {
		return nil
	}
	ents, err := d.storage.Entries(lo, end+1, 1<<30)
	if err != nil {
		if errors.Is(err, raft.ErrCompacted) {
			// Compacted prefix is in the snapshot; snapshot activation already
			// proved chunk presence at snapshot time. No additional gap.
			return nil
		}
		return err
	}
	for _, e := range ents {
		if err := d.entryGap(e); err != nil {
			return err
		}
	}
	return nil
}

// StepMsg delivers a raft message to this replica. Vote-grant is NOT gated
// by chunk gaps (a promoted-but-unapplied voter must still vote, G-2).
func (d *Driver) StepMsg(ctx context.Context, msg raftpb.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.node.Step(ctx, msg)
}

// Propose proposes raw data.
func (d *Driver) Propose(ctx context.Context, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.node.Propose(ctx, data)
}

// Campaign gates on chunk presence: refuse if committed manifests lack data.
func (d *Driver) Campaign(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkChunkGaps(); err != nil {
		return fmt.Errorf("driver: campaign refused: %w", err)
	}
	return d.node.Campaign(ctx)
}

func (d *Driver) checkChunkGaps() error {
	return d.ChunkGap()
}

// ProposeConfChange proposes a joint-consensus membership change (T07).
// Applied entries of type EntryConfChange/EntryConfChangeV2 must be passed
// to ApplyConfChange on every replica (daemon apply loop).
func (d *Driver) ProposeConfChange(ctx context.Context, cc raftpb.ConfChange) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkChunkGaps(); err != nil {
		return fmt.Errorf("driver: conf change refused (chunk gap): %w", err)
	}
	return d.node.ProposeConfChange(ctx, cc)
}

// ProposeConfChangeV2 proposes a ConfChangeV2 (joint promotion).
func (d *Driver) ProposeConfChangeV2(ctx context.Context, cc raftpb.ConfChangeV2) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkChunkGaps(); err != nil {
		return fmt.Errorf("driver: conf change refused (chunk gap): %w", err)
	}
	return d.node.ProposeConfChange(ctx, cc)
}

// ApplyConfChange applies a committed ConfChange entry and updates the
// local Raft config. Returns error if the entry is not a conf change.
func (d *Driver) ApplyConfChange(e raftpb.Entry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch e.Type {
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(e.Data); err != nil {
			return fmt.Errorf("driver: unmarshal ConfChange: %w", err)
		}
		d.node.ApplyConfChange(cc)
		return nil
	case raftpb.EntryConfChangeV2:
		var v2 raftpb.ConfChangeV2
		if err := v2.Unmarshal(e.Data); err != nil {
			return fmt.Errorf("driver: unmarshal ConfChangeV2: %w", err)
		}
		d.node.ApplyConfChange(v2)
		return nil
	default:
		return fmt.Errorf("driver: entry %d type %v is not a conf change", e.Index, e.Type)
	}
}

// ReadIndex requests a linearizable read marker.
func (d *Driver) ReadIndex(ctx context.Context) error {
	return d.node.ReadIndex(ctx, nil)
}

// Stop stops the raft node and the WAL.
func (d *Driver) Stop() error {
	d.node.Stop()
	return d.storage.Close()
}

// LeaderID mirrors raft status.
func (d *Driver) LeaderID() uint64 { return d.node.Status().Lead }

// Config exposes the immutable config.
func (d *Driver) Config() raft.Config { return d.config }
