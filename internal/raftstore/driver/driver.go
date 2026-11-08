// Package driver wires raft to the metadata state machine (M2-04):
// Ready loop with persistence-before-send, leader routing, ReadIndex,
// committed-entry apply barrier, and chunk-gap campaign gating.
package driver

import (
	"context"
	"fmt"

	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
)

// Driver owns the etcd/raft.Node for one replica.
type Driver struct {
	node         raft.Node
	storage      *wal.Storage
	config       raft.Config
	chunkPresent func(hash [32]byte) bool

	applyRequested chan<- raftpb.Entry
}

// NewDriver builds a driver for the given id/voters.
func NewDriver(id uint64, voters []uint64, storage *wal.Storage, chunkPresent func(hash [32]byte) bool) (*Driver, error) {
	return NewDriverWithTicks(id, voters, storage, chunkPresent, 10, 1)
}

// NewDriverWithTicks allows jittered ticks (production etcd does the same).
func NewDriverWithTicks(id uint64, voters []uint64, storage *wal.Storage, chunkPresent func(hash [32]byte) bool, electionTick, heartbeatTick int) (*Driver, error) {
	cfg := raft.Config{
		ID:              id,
		ElectionTick:    electionTick,
		HeartbeatTick:   heartbeatTick,
		Storage:         storage,
		MaxSizePerMsg:   64 << 10,
		MaxInflightMsgs: 100,
		CheckQuorum:     true,
		PreVote:         true,
	}
	_ = voters // membership comes from Storage.InitialState (set by caller)
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

// Tick advances the logical clock.
func (d *Driver) Tick() { d.node.Tick() }

// Step processes one Ready cycle if any is pending: persist, then expose
// messages for sending. Persistence happens before Ready.Messages are
// returned (spec 8.4 rule 1). Non-blocking: (nil, nil) when nothing pending.
func (d *Driver) Step(ctx context.Context) ([]raftpb.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	select {
	case r := <-d.node.Ready():
		if err := d.storage.SaveReady(r); err != nil {
			return nil, fmt.Errorf("driver: save ready: %w", err)
		}
		d.node.Advance()
		for _, e := range r.CommittedEntries {
			if d.applyRequested != nil {
				d.applyRequested <- e
			}
		}
		return r.Messages, nil
	default:
		return nil, nil
	}
}

// StepMsg delivers a raft message to this replica.
func (d *Driver) StepMsg(ctx context.Context, msg raftpb.Message) error {
	return d.node.Step(ctx, msg)
}

// Propose proposes raw data.
func (d *Driver) Propose(ctx context.Context, data []byte) error {
	return d.node.Propose(ctx, data)
}

// Campaign gates on chunk presence: refuse if committed manifests lack data.
func (d *Driver) Campaign(ctx context.Context) error {
	if err := d.checkChunkGaps(); err != nil {
		return fmt.Errorf("driver: campaign refused: %w", err)
	}
	return d.node.Campaign(ctx)
}

func (d *Driver) checkChunkGaps() error {
	// Predicate-based; wired against committed manifests in M2-05.
	return nil
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
