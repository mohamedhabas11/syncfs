// Package locks implements cluster-coherent flock and POSIX byte-range lock
// state (M2-06). flock and fcntl/OFD locks are distinct families.
package locks

import (
	"context"
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
	// ErrDeadlock is returned when a wait would create a cycle (EDEADLK).
	ErrDeadlock = errors.New("locks: deadlock detected")
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

// queueKey separates wait queues by (Inode, Family): flock waiters never
// head-of-line block fcntl/OFD waiters on the same inode.
type queueKey struct {
	inode  [16]byte
	family int
}

// Manager owns lock state.
type Manager struct {
	mu     sync.Mutex
	byID   map[[16]byte][]Lock
	clock  func() time.Time
	queues map[queueKey][]*waiter
}

type waiter struct {
	key   string
	lock  Lock
	ready chan struct{}
}

// NewManager builds a manager.
func NewManager() *Manager {
	return &Manager{
		byID:   map[[16]byte][]Lock{},
		clock:  time.Now,
		queues: map[queueKey][]*waiter{},
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
		if held.Owner == l.Owner && held.Family == l.Family && held.Start == l.Start && held.End == l.End && held.Mode == l.Mode {
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
		if h.Owner == l.Owner && h.Family == l.Family && overlap(h, l) {
			// Superseded (including exact-range upgrades: the old entry is
			// dropped; exact idempotence is handled by the caller).
			continue
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

// BlockingGrant waits with fair queuing until the range is free. It is the
// unbounded variant (no deadline) kept for compatibility; prefer
// BlockingGrantCtx in new callers (cancellable, EDEADLK-aware).
func (m *Manager) BlockingGrant(l Lock) error {
	return m.BlockingGrantCtx(context.Background(), l)
}

// BlockingGrantCtx waits with fair queuing until the range is free or ctx is
// done. Returns ctx.Err() on cancellation, ErrDeadlock if the wait would
// form a cycle, or ErrWouldBlock-free success (nil) once granted.
func (m *Manager) BlockingGrantCtx(ctx context.Context, l Lock) error {
	key := queueKey{inode: l.InodeID, family: l.Family}
	w := &waiter{key: l.Owner, lock: l, ready: make(chan struct{})}
	m.mu.Lock()
	if m.wouldDeadlockLocked(l) {
		m.mu.Unlock()
		return ErrDeadlock
	}
	m.queues[key] = append(m.queues[key], w)
	m.mu.Unlock()

	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			if q := m.queues[key]; len(q) > 0 {
				out := q[:0]
				for _, x := range q {
					if x != w {
						out = append(out, x)
					}
				}
				m.queues[key] = out
			}
			m.mu.Unlock()
			return ctx.Err()
		case <-w.ready:
			// Nudge from Release/ApplyReleases: fall through to the attempt below.
		case <-ticker.C:
		}
		m.mu.Lock()
		// fairness: only the head of the queue may attempt Try
		if q := m.queues[key]; len(q) > 0 && q[0] == w {
			ok := m.tryLocked(l)
			if ok {
				m.queues[key] = m.queues[key][1:]
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

// wouldDeadlockLocked reports whether granting/waiting for l would close a
// wait-for cycle: l's owner waits on holders whose wait chains lead back to
// l's owner. Caller holds m.mu.
func (m *Manager) wouldDeadlockLocked(l Lock) bool {
	// Direct holders blocking l.
	blockers := map[string]bool{}
	for _, held := range m.byID[l.InodeID] {
		if held.Owner != l.Owner && conflicting(held, l) {
			blockers[held.Owner] = true
		}
	}
	// Queued waiters ahead of l that conflict with l also block l (FIFO).
	key := queueKey{inode: l.InodeID, family: l.Family}
	for _, w := range m.queues[key] {
		if w.key != l.Owner && conflicting(w.lock, l) {
			blockers[w.key] = true
		}
	}
	// BFS over wait-for edges from blockers: X waits on Y if X's queued lock
	// conflicts with a lock held by Y. Cycle iff l.Owner is reachable.
	seen := map[string]bool{}
	queue := make([]string, 0, len(blockers))
	for b := range blockers {
		queue = append(queue, b)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == l.Owner {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		// What does cur wait on?
		for _, w := range m.queues[queueKey{inode: l.InodeID, family: l.Family}] {
			if w.key != cur {
				continue
			}
			for _, held := range m.byID[w.lock.InodeID] {
				if held.Owner != cur && conflicting(held, w.lock) && held.Owner == l.Owner {
					return true
				}
				if held.Owner != cur && !seen[held.Owner] {
					queue = append(queue, held.Owner)
				}
			}
		}
	}
	return false
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
	key := queueKey{inode: l.InodeID, family: l.Family}
	if q := m.queues[key]; len(q) > 0 {
		select {
		case <-q[0].ready:
		default:
			close(q[0].ready)
		}
	}
}

// ApplyReleases drops all locks of an owner (session expiry, in Raft order).
// It also nudges every queue head (wake) so waiters re-attempt promptly.
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
	for _, q := range m.queues {
		if len(q) == 0 {
			continue
		}
		select {
		case <-q[0].ready:
		default:
			close(q[0].ready)
		}
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
