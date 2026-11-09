// Package audit is the append-only audit sink for membership,
// certificate, volume configuration, and recovery actions (spec §9).
//
// Records form a hash chain: each record stores prev_hash (hex) so
// tampering with history is detectable by Verify.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/auth"
)

var defaultRedact = auth.NewRedactor().Redact

// Record is one audit entry.
type Record struct {
	Seq      uint64    `json:"seq"`
	TS       string    `json:"ts"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Detail   string    `json:"detail,omitempty"`
	PrevHash string    `json:"prev_hash"`
}

// Sink appends records thread-safely.
type Sink struct {
	mu      sync.Mutex
	recs    []Record
	redactor func(string) string // optional; locks cycle
}

// NewSink builds an empty sink with secret redaction enabled.
func NewSink() *Sink { return &Sink{redactor: defaultRedact} }

// SetRedactor overrides the redaction function (tests).
func (s *Sink) SetRedactor(f func(string) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redactor = f
}

// Record appends a record, sealing it with the previous record's hash.
// Detail is redacted before storage so secrets never enter the chain.
func (s *Sink) Record(actor, action, target, detail string) Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.redactor != nil {
		detail = s.redactor(detail)
	}
	prev := ""
	if len(s.recs) > 0 {
		prev = hash(s.recs[len(s.recs)-1])
	}
	r := Record{
		Seq: uint64(len(s.recs)), TS: time.Now().UTC().Format(time.RFC3339Nano),
		Actor: actor, Action: action, Target: target, Detail: detail, PrevHash: prev,
	}
	s.recs = append(s.recs, r)
	return r
}

// Records returns a copy of all records.
func (s *Sink) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.recs...)
}

// Verify checks the hash chain (prev_hash links) from genesis.
func (s *Sink) Verify() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := ""
	for i, r := range s.recs {
		if r.Seq != uint64(i) {
			return fmt.Errorf("audit: seq gap at %d", i)
		}
		if r.PrevHash != prev {
			return fmt.Errorf("audit: prev_hash mismatch at %d", i)
		}
		prev = hash(r)
	}
	return nil
}

// WriteJSONL persists records as JSON lines (append-only semantics).
func (s *Sink) WriteJSONL(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range s.recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return f.Sync()
}

func hash(r Record) string {
	b, _ := json.Marshal(Record{
		Seq: r.Seq, TS: r.TS, Actor: r.Actor, Action: r.Action, Target: r.Target, Detail: r.Detail, PrevHash: r.PrevHash,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
