package cache

import (
	"testing"
)

func TestLeaseGuardedPut(t *testing.T) {
	c := NewCache()
	e := Entry{InodeID: [16]byte{9}, Version: 1, Data: []byte("v1")}
	if c.Put(1, e) {
		t.Fatalf("put without lease must be refused")
	}
	c.SetLease(1, true)
	if !c.Put(1, e) {
		t.Fatalf("put with lease must succeed")
	}
	got, ok := c.Get([16]byte{9})
	if !ok || string(got.Data) != "v1" {
		t.Fatalf("get: %+v", got)
	}
	c.InvalidateAll()
	if _, ok := c.Get([16]byte{9}); ok {
		t.Fatalf("InvalidateAll must clear all")
	}
}
