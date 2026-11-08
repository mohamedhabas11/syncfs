package lease

import (
	"testing"
	"time"
)

func TestGrantRevokeAck(t *testing.T) {
	m := NewManager(50 * time.Millisecond)
	inode := [16]byte{1}
	l := m.Grant(inode, 3)
	if !m.Valid(l) {
		t.Fatalf("fresh lease must be valid")
	}
	if err := m.Revoke(l.ID); err != nil {
		t.Fatal(err)
	}
	if m.Valid(l) {
		t.Fatalf("revoked lease must be invalid")
	}
	if err := m.Ack(l.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Ack(999); err == nil {
		t.Fatalf("unknown ack must fail")
	}
	l2 := m.Grant(inode, 4)
	// after TTL
	time.Sleep(60 * time.Millisecond)
	if m.Valid(l2) {
		t.Fatalf("expired lease must be invalid")
	}
}

func TestWaitClear(t *testing.T) {
	m := NewManager(30 * time.Millisecond)
	inode := [16]byte{7}
	l := m.Grant(inode, 1)
	_ = m.Revoke(l.ID)
	// no ack but lease expires at TTL → wait clears bounded by expiry (<= ttl+slack)
	t0 := time.Now()
	if err := m.WaitClear(inode); err != nil {
		t.Fatalf("expiry must clear bounded wait: %v", err)
	}
	if el := time.Since(t0); el > 100*time.Millisecond {
		t.Fatalf("WaitClear unbounded: %v", el)
	}
	// ack path clears immediately
	l2 := m.Grant(inode, 2)
	_ = m.Revoke(l2.ID)
	_ = m.Ack(l2.ID)
	if err := m.WaitClear(inode); err != nil {
		t.Fatalf("acked leases clear: %v", err)
	}
}
