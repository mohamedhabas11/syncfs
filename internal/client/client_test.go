package client

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/client/cache"
)

func TestJournalRecordLookup(t *testing.T) {
	j := NewJournal()
	op := [16]byte{1}
	if _, ok := j.Lookup(op); ok {
		t.Fatalf("unknown must not be found")
	}
	j.Record(op, OpCommitted)
	if s, ok := j.Lookup(op); !ok || s != OpCommitted {
		t.Fatalf("recorded: %v %v", s, ok)
	}
}

func TestSessionInvalidateAllOnDrop(t *testing.T) {
	j := NewJournal()
	c := cache.NewCache()
	c.SetLease(1, true)
	_ = c.Put(1, cache.Entry{InodeID: [16]byte{3}, Version: 1, Data: []byte("x")})
	s := NewSession(j, c)
	aborted := false
	s.Drop(func() error { aborted = true; return nil })
	if !aborted {
		t.Fatalf("abort hook must fire on drop")
	}
	if _, ok := c.Get([16]byte{3}); ok {
		t.Fatalf("cache must be invalidated on drop (Inv.6)")
	}
	if s.Check() == nil {
		t.Fatalf("dropped session must fail Check()")
	}
	// reconnect then query retained op
	j.Record([16]byte{9}, OpRejected)
	if _, err := s.Reconnect(nil); err == nil {
		t.Fatalf("reconnect without lookup must fail")
	}
	if _, err := s.Reconnect(func(id [16]byte) (OpState, bool) { return j.Lookup(id) }); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if st, ok := s.Query([16]byte{9}); !ok || st != OpRejected {
		t.Fatalf("query after reconnect: %v %v", st, ok)
	}
	if s.Check() != nil {
		t.Fatalf("after reconnect session must be healthy")
	}
}
