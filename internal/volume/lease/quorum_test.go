package lease

import (
	"testing"
	"time"
)

func TestGrantWithoutQuorumRefused(t *testing.T) {
	m := NewManager(time.Minute)
	m.SetQuorumFunc(func() bool { return false })
	if l := m.Grant([16]byte{1}, 1); l != nil {
		t.Fatalf("minority grant must return nil, got %+v", l)
	}
	m.SetQuorumFunc(func() bool { return true })
	if l := m.Grant([16]byte{1}, 1); l == nil {
		t.Fatalf("quorum grant must succeed")
	}
}
