// Package locks implements cluster-coherent flock and POSIX byte-range lock
// state (M2-06). flock and fcntl/OFD locks are distinct families.
package locks

import (
	"errors"
	"fmt"
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

// conflicting reports whether two ranges/modes clash. Family separation is
// total (POSIX lock families have distinct delivery): flock and fcntl never
// interfere with each other even on the same inode+range.
func conflicting(a, b Lock) bool {
	if a.InodeID != b.InodeID || a.Family != b.Family {
		return false
	}
	// disjoint ranges never clash
	if (a.End != 0 && a.End <= b.Start) || (b.End != 0 && b.End <= a.Start) {
		return false
	}
	// Same owner never blocks itself (POSIX merges/converts same-process locks).
	if a.Owner == b.Owner {
		// an old overlapping same-owner lock is superseded by the newcomer
		return false
	}
	// shared-shared is fine; any exclusive clashes
	return a.Mode == Exclusive || b.Mode == Exclusive
}

// Try locks non-blocking; ErrWouldBlock if held by anyone else.
// Same-owner overlapping locks supersede (merge/convert); never self-block.
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
	// POSIX merge/convert: drop same-owner overlapping locks being superseded.
	m.byID[l.InodeID] = purgeSameOwnerOverlapLocked(m.byID[l.InodeID], l)
	m.byID[l.InodeID] = append(m.byID[l.InodeID], l)
	return nil
}

func purgeSameOwnerOverlapLocked(held []Lock, l Lock) []Lock {
	if len(held) == 0 {
		return held
	}
	out := held[:0]
	for _, h := range held {
		if h.Owner == l.Owner && h.Family == l.Family &&
			!(h.Start == l.Start && h.End == l.End) {
			// overlapping ranges supersede; disjoint or exact idempotence are kept.
			if overlap(h, l) {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}

func overlap(a, b Lock) bool {
	if a.End != 0 && a.End <= b.Start {
		return false
	}
	if b.End != 0 && b.End <= a.Start {
		return false
	}
	return true
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
	m.byID[l.InodeID] = purgeSameOwnerOverlapLocked(m.byID[l.InodeID], l)
	m.byID[l.InodeID] = append(m.byID[l.InodeID], l)
	return true
}

// Release unlocks one owner+range+mode and nudges the queue head
// (the head dequeues itself when it acquires — no starvation).
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
		select {
		case <-q[0].ready:
		default:
			close(q[0].ready)
		}
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
		var ino [16]byte
		copy(ino[:], p.InodeId)
		return m.Try(Lock{InodeID: ino, Family: int(p.Family), Owner: p.OwnerKey, Start: p.RangeStart, End: p.RangeEnd, Mode: int(p.Mode)})
	}
	if cmd.Kind == syncfsapi.CommandKind_LOCK_RELEASE {
		p := cmd.GetLockRelease()
		var ino [16]byte
		copy(ino[:], p.InodeId)
		// Release all locks of this owner on this inode (full-width).
		m.mu.Lock()
		held := m.byID[ino]
		out := held[:0]
		for _, h := range held {
			if h.Owner != p.OwnerKey {
				out = append(out, h)
			}
		}
		m.byID[ino] = out
		m.mu.Unlock()
		return nil
	}
	return nil
}
