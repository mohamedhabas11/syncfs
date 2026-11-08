package fusefs

import (
	"context"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// TestNilHandlerFailsClosed exercises the M3-01 hardening: an uninitialized
// Handler (&Handler{} as used by smoke tests) must return ENOENT, not panic.
func TestNilHandlerFailsClosed(t *testing.T) {
	r := &Root{h: &Handler{}}
	var out fuse.AttrOut
	if e := r.Getattr(context.Background(), nil, &out); e != syscall.ENOENT {
		t.Fatalf("Getattr on nil root want ENOENT, got %v", e)
	}
	var entry fuse.EntryOut
	if _, e := r.Lookup(context.Background(), "x", &entry); e != syscall.ENOENT {
		t.Fatalf("Lookup on nil root want ENOENT, got %v", e)
	}
}

// TestOpenRejectsODIRECT verifies strict-mode O_DIRECT handling (Linux only;
// off-Linux the flag constant is 0 and Open proceeds as on the dev host).
func TestOpenRejectsODIRECT(t *testing.T) {
	if oDirectFlag == 0 {
		t.Skip("O_DIRECT is Linux-only; dev host skips")
	}
	h := NewHandler("v")
	r := &Root{h: h}
	if _, _, e := r.Open(context.Background(), oDirectFlag|syscall.O_RDWR); e != syscall.EINVAL {
		t.Fatalf("O_DIRECT must be EINVAL in strict mode, got %v", e)
	}
	if _, _, e := r.Open(context.Background(), syscall.O_RDWR); e != 0 {
		t.Fatalf("O_RDWR without O_DIRECT must succeed, got %v", e)
	}
}

// TestStrictHandlerFailsClosed verifies a production (nil-syncer) handler
// returns EIO instead of silently acknowledging writes (Inv.10).
func TestStrictHandlerFailsClosed(t *testing.T) {
	h := NewStrictHandler("v")
	if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	if _, e := h.WriteFile("f", 0, []byte("x")); e != syscall.EIO {
		t.Fatalf("strict write without coordinator must be EIO, got %v", e)
	}
}
