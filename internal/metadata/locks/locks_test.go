package locks

import (
	"errors"
	"testing"
)

var ino = [16]byte{5}

func TestTryAndConflict(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessA", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(a); err != nil {
		t.Fatal(err)
	}
	b := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessB", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(b); !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("conflict must be WouldBlock, got %v", err)
	}
	// shared-only ranges don't clash
	s := Lock{InodeID: ino, Family: FamilyFcntl, Owner: "sessB", Mode: Shared, Start: 200, End: 300}
	if err := m.Try(s); err != nil {
		t.Fatalf("non-conflicting shared must pass: %v", err)
	}
	// release then lock
	m.Release(a)
	if err := m.Try(b); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestSessionExpiryDropsLocks(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessA", Mode: Exclusive}
	b := Lock{InodeID: ino, Family: FamilyOFD, Owner: "sessB", Mode: Exclusive, Start: 500, End: 600}
	_ = m.Try(a)
	_ = m.Try(b)
	m.ApplyReleases("sessA")
	c := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessC", Mode: Exclusive}
	if err := m.Try(c); err != nil {
		t.Fatalf("sessC after sessA expiry: %v", err)
	}
}
