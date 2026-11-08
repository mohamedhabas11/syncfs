package lease

import (
	"testing"
	"time"
)

func TestValidFailsOnStaleGeneration(t *testing.T) {
	m := NewManager(time.Minute)
	inode := [16]byte{1}
	l := m.Grant(inode, 3)
	if !m.Valid(l) {
		t.Fatalf("fresh lease must be valid")
	}
	m.SetCurrentGeneration(inode, 4)
	if m.Valid(l) {
		t.Fatalf("stale-generation lease must be invalid after generation bump")
	}
	l2 := m.Grant(inode, 4)
	if !m.Valid(l2) {
		t.Fatalf("current-generation lease must be valid")
	}
}
