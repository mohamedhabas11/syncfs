// Package fault provides a Linux three-node fault-injection harness (M4-04):
// process kill, pause, network partition, packet delay/duplication,
// disk-full, corrupted pack, and FUSE-client restart. The interface is the
// contract M5 qualification runs against; the Docker realization lands there.
package fault

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// FaultKind names an injectable fault.
type FaultKind int

const (
	FaultKill FaultKind = iota
	FaultPause
	FaultPartition
	FaultDelay
	FaultDuplicate
	FaultDiskFull
	FaultCorruptPack
	FaultClientRestart
)

// Fault is one knobs-set entry.
type Fault struct {
	Kind     FaultKind
	NodeID   uint64
	Duration time.Duration
	// DelayMs / DupCount applied for partition-ish kinds
	DelayMs  int
	DupCount int
}

// Harness applies faults to the mesh and tracks their history.
type Harness struct {
	mu      sync.Mutex
	active  []Fault
	applied []Fault
	clock   func() time.Time
}

// NewHarness builds a harness.
func NewHarness() *Harness {
	return &Harness{clock: time.Now}
}

// Apply records a fault synchronously; return reflects injected state.
func (h *Harness) Apply(f Fault) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f.Kind < FaultKill || f.Kind > FaultClientRestart {
		return fmt.Errorf("fault: unknown kind %d", f.Kind)
	}
	h.active = append(h.active, f)
	h.applied = append(h.applied, f)
	return nil
}

// Clear removes faults for a node (recovery step in fault tests).
func (h *Harness) Clear(nodeID uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.active[:0]
	for _, f := range h.active {
		if f.NodeID != nodeID {
			kept = append(kept, f)
		}
	}
	h.active = kept
}

// Partitioned reports whether `a` is cut from `b` under current faults.
func (h *Harness) Partitioned(a, b uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.active {
		if f.Kind == FaultPartition && (f.NodeID == a || f.NodeID == b) {
			return true
		}
	}
	return false
}

// History returns the applied-fault log.
func (h *Harness) History() []Fault {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Fault(nil), h.applied...)
}

// Active returns currently active faults.
func (h *Harness) Active() []Fault {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Fault(nil), h.active...)
}

// IsRealizable reports whether a fault can be exercised by the mesh harness
// (all kinds must be; the docker realization refines delays).
func (h *Harness) IsRealizable(f Fault) (bool, error) {
	if f.Kind < FaultKill || f.Kind > FaultClientRestart {
		return false, errors.New("fault: unknown kind cannot be realized")
	}
	return true, nil
}
