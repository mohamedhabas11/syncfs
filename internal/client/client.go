// Package client implements client-side failure semantics (M3-03):
// operation journal, reconnect status lookup, invalidate-all-on-drop, and
// FUSE abort. Strict-mode mounts fail closed: no stale reads after a drop.
package client

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// Retention: N=10,000 entries, 10-minute expiry (spec §8.2 retention).
// The minimum durable form is a single atomically-rewritten JSON file
// (tmp + rename + fsync) in the journal dir; it survives client restart so
// unknown ops are never silently retried blindly.
type Journal struct {
	mu        sync.Mutex
	ops       map[[16]byte]entry
	expired   map[[16]byte]bool
	retention int
	ttl       time.Duration
	path      string
	// clock is the time source (tests override for TTL).
	clock func() time.Time
}

type entry struct {
	state OpState
	at    time.Time
}

// NewJournal builds a journal with the default retention (M1-02 numbers).
func NewJournal() *Journal {
	return &Journal{ops: map[[16]byte]entry{}, expired: map[[16]byte]bool{}, retention: 10000, ttl: 10 * time.Minute, clock: time.Now}
}

// NewJournalWithDir builds a journal persisted under dir (durable across
// client restarts). Missing dir/file starts empty.
func NewJournalWithDir(dir string) (*Journal, error) {
	if dir == "" {
		return nil, errors.New("client: empty journal dir")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	j := NewJournal()
	j.path = filepath.Join(dir, "journal.json")
	data, err := os.ReadFile(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return j, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return j, nil
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("client: journal corrupt: %w", err)
	}
	for key, state := range raw {
		id, err := decodeOpID(key)
		if err != nil {
			return nil, err
		}
		j.ops[id] = entry{state: OpState(state), at: time.Now()}
	}
	return j, nil
}

// Record stores or refreshes an operation's outcome (in-memory; durable
// when the journal was opened with a dir).
func (j *Journal) Record(opID [16]byte, state OpState) {
	_ = j.RecordE(opID, state)
}

// RecordE stores the state and, when a dir is configured, atomically
// persists before returning (fail-closed on I/O error).
func (j *Journal) RecordE(opID [16]byte, state OpState) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked()
	prev, hadPrev := j.ops[opID]
	j.ops[opID] = entry{state: state, at: time.Now()}
	if j.path == "" {
		return nil
	}
	if err := j.persistLocked(); err != nil {
		if hadPrev {
			j.ops[opID] = prev
		} else {
			delete(j.ops, opID)
		}
		return err
	}
	return nil
}

func decodeOpID(key string) ([16]byte, error) {
	var id [16]byte
	b, err := hex.DecodeString(key)
	if err != nil {
		return id, err
	}
	if len(b) != 16 {
		return id, fmt.Errorf("client: bad op id length %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}

func (j *Journal) persistLocked() error {
	raw := map[string]string{}
	for id, e := range j.ops {
		raw[hex.EncodeToString(id[:])] = string(e.state)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, j.path); err != nil {
		return err
	}
	// Best-effort directory sync so the rename itself is durable.
	if dir, err := os.Open(filepath.Dir(j.path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

// Lookup returns the retained state (false = never seen/expired).
func (j *Journal) Lookup(opID [16]byte) (OpState, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.ops[opID]
	if !ok {
		return OpUnknown, false
	}
	if j.clock != nil && e.at.Add(j.ttl).Before(j.clock()) {
		delete(j.ops, opID)
		j.expired[opID] = true
		return OpUnknown, false
	}
	return e.state, true
}

func (j *Journal) pruneLocked() {
	now := time.Now()
	if j.clock != nil {
		now = j.clock()
	}
	// TTL sweep: evict entries older than the retention window first.
	for id, e := range j.ops {
		if e.at.Add(j.ttl).Before(now) {
			delete(j.ops, id)
			j.expired[id] = true
		}
	}
	// Capacity sweep: evict oldest first until under retention.
	for len(j.ops) >= j.retention {
		oldest := [16]byte{}
		var oldestT time.Time
		first := true
		for id, e := range j.ops {
			if first || e.at.Before(oldestT) {
				oldest, oldestT = id, e.at
				first = false
			}
		}
		delete(j.ops, oldest)
		j.expired[oldest] = true
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

// Reconnect re-establishes the control path and looks up retained ops
// against the server via lookup. Unknown ops are left unknown (safe to
// retry); committed/rejected results are folded back into the journal.
func (s *Session) Reconnect(lookup func(opID [16]byte) (OpState, bool)) (int, error) {
	if lookup == nil {
		return 0, errors.New("client: no lookup provided")
	}
	s.mu.Lock()
	s.connected = true
	s.reconnects++
	count := s.reconnects
	s.mu.Unlock()
	// Collect op IDs first, then look up without holding the session lock
	// to avoid lock-order surprises (lookup may touch the journal).
	s.journal.mu.Lock()
	ids := make([][16]byte, 0, len(s.journal.ops))
	for id := range s.journal.ops {
		ids = append(ids, id)
	}
	s.journal.mu.Unlock()
	for _, id := range ids {
		state, found := lookup(id)
		if found && state != OpUnknown {
			s.journal.mu.Lock()
			s.journal.ops[id] = entry{state: state, at: time.Now()}
			s.journal.mu.Unlock()
		}
	}
	return count, nil
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
