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
type EnrollmentStore interface {
	Put(rec EnrollmentRecord) error
	Get(nodeID string) (EnrollmentRecord, bool)
	MarkConsumed(nodeID string, at time.Time) error
}

// MemoryStore is the M1-03 in-memory implementation (tests + unit work).
type MemoryStore struct {
	mu   sync.Mutex
	recs map[string]EnrollmentRecord
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[string]EnrollmentRecord{}} }

func (s *MemoryStore) Put(rec EnrollmentRecord) error {
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
	r.ConsumedAt = at
	s.recs[nodeID] = r
	return nil
}

// Fingerprint is the hex SHA-256 of a DER cert.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// Consume validates and atomically burns a one-time record.
// Errors: unknown node, expired, already consumed, fingerprint/nonce mismatch.
func Consume(store EnrollmentStore, nodeID, fingerprint, nonce string, now time.Time) error {
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
