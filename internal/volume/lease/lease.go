// Package lease implements ReadIndex-backed read leases with revocation and
// ack (M2-06). Three leases must never be conflated (spec 8.3):
// (a) Raft leader lease (ReadIndex clock caveat),
// (b) client ReadLease (userspace cache authority),
// (c) POSIX lock lease — see internal/metadata/locks.
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lease is a server-issued, per-inode read lease.
type Lease struct {
	ID         uint64
	InodeID    [16]byte
	Generation uint64
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Revoked    bool
	Acked      bool
}

// Manager grants, tracks, and revokes leases.
type Manager struct {
	mu     sync.Mutex
	nextID uint64
	leases map[uint64]*Lease
	clock  func() time.Time
	ttl    time.Duration
}

// NewManager builds a manager with the given TTLs.
func NewManager(ttl time.Duration) *Manager {
	return &Manager{
		leases: map[uint64]*Lease{},
		clock:  time.Now,
		ttl:    ttl,
	}
}

// Grant issues a lease for inode at the current generation.
func (m *Manager) Grant(inode [16]byte, generation uint64) *Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	l := &Lease{
		ID:         m.nextID,
		InodeID:    inode,
		Generation: generation,
		IssuedAt:   m.clock(),
		ExpiresAt:  m.clock().Add(m.ttl),
	}
	m.leases[l.ID] = l
	return l
}

// Revoke marks the lease revoked; the holder must Ack before writers proceed
// (or the lease must expire).
func (m *Manager) Revoke(id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return errors.New("lease: unknown id")
	}
	l.Revoked = true
	return nil
}

// Ack clears the revocation wait for the holder.
func (m *Manager) Ack(id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return errors.New("lease: unknown id")
	}
	l.Acked = true
	return nil
}

// Valid reports whether a lease may still be used (not revoked, unexpired).
func (m *Manager) Valid(l *Lease) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.leases[l.ID]
	if !ok || cur.Revoked || cur.Acked {
		return false
	}
	return m.clock().Before(cur.ExpiresAt)
}

// WaitClear blocks until all leases for inode are acked, revoked-acked, or
// expired. Bounded by ttl*2 wait ceiling to avoid indefinite blocking.
func (m *Manager) WaitClear(inode [16]byte) error {
	deadline := m.clock().Add(2 * m.ttl)
	for {
		m.mu.Lock()
		pending := 0
		for _, l := range m.leases {
			if l.InodeID == inode && l.Revoked && !l.Acked && m.clock().Before(l.ExpiresAt) {
				pending++
			}
		}
		m.mu.Unlock()
		if pending == 0 {
			return nil
		}
		if m.clock().After(deadline) {
			return fmt.Errorf("lease: revocation wait exceeded (%d pending)", pending)
		}
		// short poll; production uses a condition variable
		time.Sleep(1 * time.Millisecond)
	}
}
