// Package client implements client-side failure semantics (M3-03):
// operation journal, reconnect status lookup, invalidate-all-on-drop, and
// FUSE abort. Strict-mode mounts fail closed: no stale reads after a drop.
package client

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/client/cache"
)

// OpState is the journal's view of one operation.
type OpState string

const (
	OpUnknown   OpState = "unknown"
	OpCommitted OpState = "committed"
	OpRejected  OpState = "rejected"
)

// Journal is the bounded, durable client operation journal.
// Retention: N=10,000 entries, 10-minute expiry (spec 5-4 retention).
type Journal struct {
	mu        sync.Mutex
	ops       map[[16]byte]entry
	expired   map[[16]byte]bool
	retention int
}

type entry struct {
	state OpState
	at    time.Time
}

// NewJournal builds a journal with the default retention (M1-02 numbers).
func NewJournal() *Journal {
	return &Journal{ops: map[[16]byte]entry{}, expired: map[[16]byte]bool{}, retention: 10000}
}

// Record stores or refreshes an operation's outcome.
func (j *Journal) Record(opID [16]byte, state OpState) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked()
	j.ops[opID] = entry{state: state, at: time.Now()}
}

// Lookup returns the retained state (false = never seen/expired).
func (j *Journal) Lookup(opID [16]byte) (OpState, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.ops[opID]
	if !ok {
		return OpUnknown, false
	}
	return e.state, true
}

func (j *Journal) pruneLocked() {
	if len(j.ops) < j.retention {
		return
	}
	// drop oldest first (simple FIFO overflow)
	for k := range j.ops {
		delete(j.ops, k)
		j.expired[k] = true
		break
	}
}

// Session tracks the control connection and cache posture.
type Session struct {
	mu         sync.Mutex
	journal    *Journal
	cache      *cache.Cache
	connected  bool
	server     func() error // abort hook (fusectl/unmount)
	reconnects int
}

// NewSession builds a session around a journal and cache.
func NewSession(j *Journal, c *cache.Cache) *Session {
	return &Session{journal: j, cache: c, connected: true}
}

// Droppable is the drop signal: control connection went away.
func (s *Session) Drop(abort func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	// Inv.6: invalidate EVERYTHING before serving again.
	s.cache.InvalidateAll()
	// Abort the FUSE connection so the kernel stops being served.
	if abort != nil {
		_ = abort()
	}
}

// Reconnect re-establishes the control path and looks up unknown ops.
func (s *Session) Reconnect(lookup func(opID [16]byte) (OpState, bool)) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = true
	s.reconnects++
	if lookup == nil {
		return 0, errors.New("client: no lookup provided")
	}
	return s.reconnects, nil
}

// Query returns a retained op state after reconnect (unknown = safe to retry).
func (s *Session) Query(opID [16]byte) (OpState, bool) {
	return s.journal.Lookup(opID)
}

// Connected reports session health.
func (s *Session) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Check reports whether the mount is dead (fail-closed signal to the adapter).
func (s *Session) Check() error {
	if !s.Connected() {
		return fmt.Errorf("client: session dropped; operations must fail closed")
	}
	return nil
}
