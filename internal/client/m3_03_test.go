package client

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/client/cache"
)

// TestJournalPersistsAcrossRestart verifies the minimum durable journal:
// reload from the same dir retains committed states.
func TestJournalPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	j1, err := NewJournalWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if err := j1.RecordE(op, OpCommitted); err != nil {
		t.Fatal(err)
	}
	j2, err := NewJournalWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := j2.Lookup(op); !ok || st != OpCommitted {
		t.Fatalf("journal must survive restart: %v %v", st, ok)
	}
}

// TestReconnectFoldsServerState verifies reconnect actually calls lookup
// and folds server state back into the journal.
func TestReconnectFoldsServerState(t *testing.T) {
	j := NewJournal()
	c := cache.NewCache()
	s := NewSession(j, c)
	op := [16]byte{9}
	j.Record(op, OpUnknown)
	called := false
	n, err := s.Reconnect(func(id [16]byte) (OpState, bool) {
		called = true
		if id != op {
			t.Fatalf("lookup id mismatch")
		}
		return OpCommitted, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconnect count: %d", n)
	}
	if !called {
		t.Fatalf("lookup must be invoked on Reconnect")
	}
	if st, ok := s.Query(op); !ok || st != OpCommitted {
		t.Fatalf("reconnect must fold server state: %v %v", st, ok)
	}
}

// TestDropInvalidateSuspended verifies suspended/restarted posture: after
// drop, cache is empty and Check fails until reconnect.
func TestDropInvalidateSuspended(t *testing.T) {
	j := NewJournal()
	c := cache.NewCache()
	c.SetLease(1, true)
	_ = c.Put(1, cache.Entry{InodeID: [16]byte{3}, Version: 1, Data: []byte("x")})
	s := NewSession(j, c)
	s.Drop(nil)
	if _, ok := c.Get([16]byte{3}); ok {
		t.Fatalf("drop must invalidate all (suspended mount serves nothing)")
	}
	if s.Check() == nil {
		t.Fatalf("dropped session must fail closed")
	}
}
