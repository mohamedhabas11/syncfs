// Package locks implements cluster-coherent flock and POSIX byte-range lock
// state (M2-06). flock and fcntl/OFD locks are distinct families.
package locks

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
)

// Families.
const (
	FamilyFlock = 1
	FamilyFcntl = 2
	FamilyOFD   = 3
)

// Modes.
const (
	Shared    = 1
	Exclusive = 2
)

var (
	ErrWouldBlock = errors.New("locks: would block")
)

// Lock is one granted lock (one per owner key, range).
type Lock struct {
	InodeID [16]byte
	Family  int
	Owner   string // session+process lock owner string
	Start   uint64
	End     uint64 // exclusive; End==0 means EOF
	Mode    int
}

// Manager owns lock state.
type Manager struct {
	mu     sync.Mutex
	byID   map[[16]byte][]Lock
	clock  func() time.Time
	queues map[[16]byte][]*waiter
}

type waiter struct {
	key   string
	ready chan struct{}
}

// NewManager builds a manager.
func NewManager() *Manager {
	return &Manager{
		byID:   map[[16]byte][]Lock{},
		clock:  time.Now,
		queues: map[[16]byte][]*waiter{},
	}
}

// conflicting reports whether two ranges/modes clash.
func conflicting(a, b Lock) bool {
	if a.InodeID != b.InodeID {
		return false
	}
	// disjoint ranges never clash
	if (a.End != 0 && a.End <= b.Start) || (b.End != 0 && b.End <= a.Start) {
		return false
	}
	// shared-shared is fine; any exclusive clashes
	return a.Mode == Exclusive || b.Mode == Exclusive
}

// Try locks non-blocking; ErrWouldBlock if held by anyone else.
func (m *Manager) Try(l Lock) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.byID[l.InodeID] {
		if held.Owner == l.Owner && held.Start == l.Start && held.End == l.End && held.Mode == l.Mode {
			return nil // idempotent
		}
		if conflicting(held, l) {
			return fmt.Errorf("%w: held by %s mode %d", ErrWouldBlock, held.Owner, held.Mode)
		}
	}
	m.byID[l.InodeID] = append(m.byID[l.InodeID], l)
	return nil
}

// BlockingGrant waits with fair queuing until the range is free or the ctx
// expires (>= waitCap enforced by caller timeout).
func (m *Manager) BlockingGrant(l Lock) error {
	m.mu.Lock()
	w := &waiter{key: l.Owner, ready: make(chan struct{})}
	m.queues[l.InodeID] = append(m.queues[l.InodeID], w)
	m.mu.Unlock()

	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C
		m.mu.Lock()
		// fairness: only the head of the queue may attempt Try
		if len(m.queues[l.InodeID]) > 0 && m.queues[l.InodeID][0] == w {
			ok := m.tryLocked(l)
			if ok {
				m.queues[l.InodeID] = m.queues[l.InodeID][1:]
				m.usableLocked(l.InodeID)
				m.mu.Unlock()
				return nil
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) tryLocked(l Lock) bool {
	for _, held := range m.byID[l.InodeID] {
		if conflicting(held, l) {
			return false
		}
	}
	m.byID[l.InodeID] = append(m.byID[l.InodeID], l)
	return true
}

func (m *Manager) usableLocked(inode [16]byte) { sort.Slice(m.byID[inode], nil) }

// Release unlocks one owner+range+mode and wakes the queue head.
func (m *Manager) Release(l Lock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	held := m.byID[l.InodeID]
	out := held[:0]
	for _, h := range held {
		if !(h.Owner == l.Owner && h.Start == l.Start && h.End == l.End && h.Mode == l.Mode) {
			out = append(out, h)
		}
	}
	m.byID[l.InodeID] = out
	if q := m.queues[l.InodeID]; len(q) > 0 {
		close(q[0].ready)
		m.queues[l.InodeID] = q[1:]
	}
}

// ApplyReleases drops all locks of an owner (session expiry, in Raft order).
func (m *Manager) ApplyReleases(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, held := range m.byID {
		out := held[:0]
		for _, h := range held {
			if h.Owner != owner {
				out = append(out, h)
			}
		}
		m.byID[id] = out
	}
}

// Proto surfaces for the state machine boundary.
func (m *Manager) BindProto(cmd *syncfsapi.Command, idx uint64) error {
	if cmd.Kind == syncfsapi.CommandKind_LOCK_GRANT {
		p := cmd.GetLockGrant()
		return m.Try(Lock{Family: int(p.Family), Owner: p.OwnerKey, Mode: int(p.Mode)})
	}
	return nil
}
