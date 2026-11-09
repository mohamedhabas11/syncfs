package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Enrollment implements the one-time learner enrollment bundle
// (technical spec sections 9, 12.1): an admin pre-authorizes a node
// fingerprint + nonce in cluster state; the learner proves possession of its
// cert + nonce over mTLS exactly once.
type EnrollmentRecord struct {
	NodeID          string
	CertFingerprint string // hex SHA-256 of DER
	Nonce           string // hex; 32 random bytes, single-use
	ExpiresAt       time.Time
	ConsumedAt      time.Time // zero = unused
}

// EnrollmentStore abstracts the committed-state record (M4-02 wires Raft).
// Stores SHOULD implement AtomicEnrollmentStore so Consume is a single atomic
// check-and-burn; the Get+MarkConsumed fallback is best-effort and remains
// vulnerable to a TOCTOU race in the window between the two calls.
type EnrollmentStore interface {
	Put(rec EnrollmentRecord) error
	Get(nodeID string) (EnrollmentRecord, bool)
	MarkConsumed(nodeID string, at time.Time) error
}

// AtomicEnrollmentStore is a store that can consume an enrollment atomically:
// validity checks (existence, expiry, consumed, proof match) and the
// consumed-mark happen as one indivisible step, so concurrent uses of one
// bundle produce exactly one success. MemoryStore implements it.
type AtomicEnrollmentStore interface {
	TryConsume(nodeID, fingerprint, nonce string, now time.Time) error
}

// MemoryStore is the M1-03 in-memory implementation (tests + unit work).
type MemoryStore struct {
	mu   sync.Mutex
	recs map[string]EnrollmentRecord
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[string]EnrollmentRecord{}} }

func (s *MemoryStore) Put(rec EnrollmentRecord) error {
	if rec.NodeID == "" || rec.CertFingerprint == "" || rec.Nonce == "" {
		return errors.New("auth: enrollment record must have non-empty node, fingerprint, and nonce")
	}
	if rec.ExpiresAt.IsZero() {
		return errors.New("auth: enrollment record must have expiry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recs[rec.NodeID]; ok {
		return errors.New("auth: duplicate enrollment record")
	}
	s.recs[rec.NodeID] = rec
	return nil
}

func (s *MemoryStore) Get(nodeID string) (EnrollmentRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[nodeID]
	return r, ok
}

func (s *MemoryStore) MarkConsumed(nodeID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[nodeID]
	if !ok {
		return errors.New("auth: enrollment record not found")
	}
	if !r.ConsumedAt.IsZero() {
		return errors.New("auth: enrollment already consumed")
	}
	r.ConsumedAt = at
	s.recs[nodeID] = r
	return nil
}

// TryConsume validates and burns a one-time record atomically: the whole
// check-and-mark sequence runs under one lock acquisition, so racing
// concurrent uses produce exactly one success.
func (s *MemoryStore) TryConsume(nodeID, fingerprint, nonce string, now time.Time) error {
	if nodeID == "" || fingerprint == "" || nonce == "" {
		return errors.New("auth: enrollment proof must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[nodeID]
	if !ok {
		return fmt.Errorf("auth: enrollment unknown node %q", nodeID)
	}
	if !rec.ConsumedAt.IsZero() {
		return errors.New("auth: enrollment already consumed")
	}
	if now.After(rec.ExpiresAt) {
		return errors.New("auth: enrollment expired")
	}
	if !equalStrings(rec.CertFingerprint, fingerprint) || !equalStrings(rec.Nonce, nonce) {
		return errors.New("auth: enrollment proof mismatch")
	}
	rec.ConsumedAt = now
	s.recs[nodeID] = rec
	return nil
}

// Fingerprint is the hex SHA-256 of a DER cert.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// Consume validates and burns a one-time record.
// Errors: unknown node, expired, already consumed, fingerprint/nonce mismatch.
// Atomic stores (MemoryStore) consume under one lock; generic stores fall
// back to Get-then-Mark, which is NOT atomic across the window and must not
// be used for production enrollment — implement AtomicEnrollmentStore.
func Consume(store EnrollmentStore, nodeID, fingerprint, nonce string, now time.Time) error {
	if as, ok := store.(AtomicEnrollmentStore); ok {
		return as.TryConsume(nodeID, fingerprint, nonce, now)
	}
	if nodeID == "" || fingerprint == "" || nonce == "" {
		return errors.New("auth: enrollment proof must be non-empty")
	}
	rec, ok := store.Get(nodeID)
	if !ok {
		return fmt.Errorf("auth: enrollment unknown node %q", nodeID)
	}
	if !rec.ConsumedAt.IsZero() {
		return errors.New("auth: enrollment already consumed")
	}
	if now.After(rec.ExpiresAt) {
		return errors.New("auth: enrollment expired")
	}
	if !equalStrings(rec.CertFingerprint, fingerprint) || !equalStrings(rec.Nonce, nonce) {
		return errors.New("auth: enrollment proof mismatch")
	}
	return store.MarkConsumed(nodeID, now)
}

func equalStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
