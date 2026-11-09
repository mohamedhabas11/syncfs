// Package gc implements conservative mark-and-sweep chunk collection (M4-01).
//
// A chunk is collectible only when no retained metadata snapshot, committed
// manifest, in-flight operation, or valid read lease can reference it, and the
// collection epoch has advanced past the oldest retained snapshot index.
package gc

import (
	"errors"
	"sync"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
)

// Reachability is the mark root: referenced ids + pinned snapshots + leased.
type Reachability struct {
	Chunks      map[chunkstore.ChunkID]bool
	SnapshotIDs map[chunkstore.ChunkID]bool // reachable from retained snapshots
	LeasedIDs   map[chunkstore.ChunkID]bool
	InflightOp  map[chunkstore.ChunkID]bool
}

// All is the union.
func (r *Reachability) All() map[chunkstore.ChunkID]bool {
	out := map[chunkstore.ChunkID]bool{}
	for id := range r.Chunks {
		out[id] = true
	}
	for id := range r.SnapshotIDs {
		out[id] = true
	}
	for id := range r.LeasedIDs {
		out[id] = true
	}
	for id := range r.InflightOp {
		out[id] = true
	}
	return out
}

// ComposeReachability builds the pinned set from the supported pin sources:
// current manifests, retained snapshots, valid client leases, and in-flight
// operations. Callers supply each source; GC only collects outside the union.
func ComposeReachability(manifests, snapshots, leases, inflight []chunkstore.ChunkID) Reachability {
	r := Reachability{
		Chunks:      map[chunkstore.ChunkID]bool{},
		SnapshotIDs: map[chunkstore.ChunkID]bool{},
		LeasedIDs:   map[chunkstore.ChunkID]bool{},
		InflightOp:  map[chunkstore.ChunkID]bool{},
	}
	for _, id := range manifests {
		r.Chunks[id] = true
	}
	for _, id := range snapshots {
		r.SnapshotIDs[id] = true
	}
	for _, id := range leases {
		r.LeasedIDs[id] = true
	}
	for _, id := range inflight {
		r.InflightOp[id] = true
	}
	return r
}

// Collector sweeps unreachable chunks between persisted generations.
type Collector struct {
	mu      sync.Mutex
	gen     uint64
	current uint64
	epochs  map[uint64][]chunkstore.ChunkID // gen -> staged ids
}

// NewCollector starts at epoch gen.
func NewCollector(start uint64) *Collector {
	return &Collector{
		gen:     start,
		current: start,
		epochs:  map[uint64][]chunkstore.ChunkID{},
	}
}

// Stage adds an id to the current epoch.
func (c *Collector) Stage(id chunkstore.ChunkID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epochs[c.current] = append(c.epochs[c.current], id)
}

// Advance moves to the next generation (caller persists it beforehand).
func (c *Collector) Advance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen = c.current
	c.current++
}

// Sweep returns unreachable ids from generations strictly older than the
// caller's `oldestRetained` snapshot epoch bound.
func (c *Collector) Sweep(reach Reachability, oldestRetained uint64) []chunkstore.ChunkID {
	c.mu.Lock()
	defer c.mu.Unlock()
	alive := reach.All()
	var cand []chunkstore.ChunkID
	for gen, ids := range c.epochs {
		if gen >= oldestRetained {
			continue
		}
		for _, id := range ids {
			if !alive[id] {
				cand = append(cand, id)
			}
		}
		delete(c.epochs, gen)
	}
	return cand
}

// SweepAndDelete sweeps unreachable ids and deletes them from the store
// (index entry removal; pack bytes become unreachable and are reclaimed by
// later pack compaction). Callers must persist the generation before this.
func (c *Collector) SweepAndDelete(s *chunkstore.Store, reach Reachability, oldestRetained uint64) ([]chunkstore.ChunkID, error) {
	cand := c.Sweep(reach, oldestRetained)
	var deleted []chunkstore.ChunkID
	for _, id := range cand {
		if err := s.Delete(id); err != nil {
			return deleted, err
		}
		deleted = append(deleted, id)
	}
	return deleted, nil
}

// Generation reports the current epoch.
func (c *Collector) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

var _ = errors.New
