// Package cache implements the userspace versioned read cache (M2-06):
// entries keyed by (inode, version), guarded by server leases.
package cache

import (
	"sync"
)

// Entry is one cached version of an inode's metadata+manifest.
type Entry struct {
	InodeID [16]byte
	Version uint64
	Data    []byte
}

// Cache is the client-side coherent cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[[16]byte]Entry
	leases  map[uint64]bool // lease IDs currently valid
}

// NewCache builds an empty cache.
func NewCache() *Cache {
	return &Cache{
		entries: map[[16]byte]Entry{},
		leases:  map[uint64]bool{},
	}
}

// Put stores an entry only if its lease is present.
func (c *Cache) Put(leaseID uint64, e Entry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.leases[leaseID] {
		return false
	}
	c.entries[e.InodeID] = e
	return true
}

// Get returns the entry; invalid for unknown/stale versions.
func (c *Cache) Get(inode [16]byte) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[inode]
	return e, ok
}

// Invalidate drops an entry and ends its lease (revocation path).
func (c *Cache) Invalidate(inode [16]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, inode)
}

// InvalidateAll drops everything (control connection drop, spec Inv.6).
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[[16]byte]Entry{}
	c.leases = map[uint64]bool{}
}

// SetLease marks a lease present/absent.
func (c *Cache) SetLease(leaseID uint64, present bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leases[leaseID] = present
}

// Leases is a snapshot of lease validity.
func (c *Cache) Leases() map[uint64]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[uint64]bool{}
	for k, v := range c.leases {
		out[k] = v
	}
	return out
}
