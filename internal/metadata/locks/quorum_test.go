package locks

import (
	"errors"
	"testing"
)

func TestTryWithoutQuorumRefused(t *testing.T) {
	m := NewManager()
	m.SetQuorumFunc(func() bool { return false })
	l := Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "A", Start: 0, End: 100, Mode: Exclusive}
	if err := m.Try(l); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("minority grant must fail ErrNoQuorum, got %v", err)
	}
	m.SetQuorumFunc(func() bool { return true })
	if err := m.Try(l); err != nil {
		t.Fatalf("quorum grant must succeed: %v", err)
	}
}
