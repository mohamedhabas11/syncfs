package client

import (
	"testing"
	"time"
)

func TestJournalTTLExpiry(t *testing.T) {
	j := NewJournal()
	base := time.Now()
	j.clock = func() time.Time { return base }
	old := [16]byte{1}
	j.Record(old, OpCommitted)
	if _, ok := j.Lookup(old); !ok {
		t.Fatalf("fresh entry must be found")
	}
	j.clock = func() time.Time { return base.Add(11 * time.Minute) }
	if _, ok := j.Lookup(old); ok {
		t.Fatalf("expired entry must be evicted (10-minute TTL)")
	}
	if _, ok := j.Lookup(old); ok {
		t.Fatalf("expired entry must stay evicted")
	}
}

func TestJournalPrunesOldestFirst(t *testing.T) {
	j := NewJournal()
	base := time.Now()
	tick := 0
	j.clock = func() time.Time { return base.Add(time.Duration(tick) * time.Second) }
	j.retention = 3
	a, b, c, d := [16]byte{1}, [16]byte{2}, [16]byte{3}, [16]byte{4}
	tick = 1
	j.Record(a, OpCommitted)
	tick = 2
	j.Record(b, OpCommitted)
	tick = 3
	j.Record(c, OpCommitted)
	// Filling to capacity, then one more must evict the oldest (a), not random.
	tick = 4
	j.Record(d, OpCommitted)
	if _, ok := j.Lookup(a); ok {
		t.Fatalf("oldest entry a must be evicted first")
	}
	for _, id := range [][16]byte{b, c, d} {
		if _, ok := j.Lookup(id); !ok {
			t.Fatalf("entry %v must survive prune", id[0])
		}
	}
}
