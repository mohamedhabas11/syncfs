// Package cluster provides an in-memory transport mesh for raft tests (M2-04).
// Messages are delivered synchronously between drivers with no network.
package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Mesh holds N drivers and routes their messages in-process.
type Mesh struct {
	drivers map[uint64]*driver.Driver
	order   []uint64
}

// NewMesh builds drivers from a name->id mapping.
func NewMesh(ids []uint64, drivers map[uint64]*driver.Driver) *Mesh {
	return &Mesh{drivers: drivers, order: ids}
}

// Round steps every driver once, delivering immediate replies.
func (m *Mesh) Round(ctx context.Context) error {
	for _, id := range m.order {
		d := m.drivers[id]
		d.Tick()
		msgs, err := d.Step(ctx)
		if err != nil {
			return err
		}
		for _, msg := range msgs {
			if peer, ok := m.drivers[msg.To]; ok {
				_ = peer.StepMsg(ctx, msg)
			}
		}
	}
	return nil
}

// LeaderID returns the id whose driver reports itself leader, or 0.
func (m *Mesh) LeaderID() uint64 {
	for id, d := range m.drivers {
		if d.LeaderID() == id {
			return id
		}
	}
	return 0
}

// Run pumps the mesh until a condition holds or the timeout elapses.
func (m *Mesh) Until(ctx context.Context, timeout time.Duration, cond func(*Mesh) bool) error {
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("cluster: timeout")
		default:
		}
		if cond(m) {
			return nil
		}
		if err := m.Round(ctx); err != nil {
			return err
		}
	}
}

// Deliver sends one message to a peer (test repair/partition control).
func (m *Mesh) Deliver(ctx context.Context, msg raftpb.Message) {
	if peer, ok := m.drivers[msg.To]; ok {
		_ = peer.StepMsg(ctx, msg)
	}
}
