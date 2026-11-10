// Package cluster provides an in-memory transport mesh for raft tests (M2-04).
// Messages are delivered synchronously between drivers with no network.
package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"github.com/mohamedhabas11/syncfs/internal/testkit/fault"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Mesh holds N drivers and routes their messages in-process.
type Mesh struct {
	drivers map[uint64]*driver.Driver
	order   []uint64
	harness *fault.Harness // optional fault injection (partition/kill/pause)
	roundN  int            // rotor for single-node-per-round ticking
}

// NewMesh builds drivers from a name->id mapping.
func NewMesh(ids []uint64, drivers map[uint64]*driver.Driver) *Mesh {
	return &Mesh{drivers: drivers, order: ids}
}

// SetFaults wires fault injection into message delivery and node stepping.
func (m *Mesh) SetFaults(h *fault.Harness) { m.harness = h }

func (m *Mesh) faulted(id uint64) bool {
	if m.harness == nil {
		return false
	}
	for _, f := range m.harness.Active() {
		if f.NodeID == id && (f.Kind == fault.FaultKill || f.Kind == fault.FaultPause) {
			return true
		}
	}
	return false
}

// Round steps the mesh once. Only one driver ticks per round (rotating);
// concurrent ticks let every node self-vote and re-lock the PreVote
// election forever (etcd interaction-harness pattern).
func (m *Mesh) Round(ctx context.Context) error {
	tickID := m.order[m.roundN%len(m.order)]
	m.roundN++
	if !m.faulted(tickID) {
		m.drivers[tickID].Tick()
	}
	for _, id := range m.order {
		if m.faulted(id) {
			continue // killed/paused node neither ticks nor steps
		}
		d := m.drivers[id]
		msgs, err := d.Step(ctx)
		if err != nil {
			return err
		}
		for _, msg := range msgs {
			if m.harness != nil && m.harness.Partitioned(id, msg.To) {
				continue // partition drops this edge (both directions via Partitioned)
			}
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
