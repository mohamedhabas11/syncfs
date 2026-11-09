package fusefs

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

// TestFsyncTruncateFailClosed verifies durability fail-closed behavior: with
// no coordinator wired (strict handler), Fsync/Truncate/FsyncDir must return
// EIO and leave state untouched, never a false durable receipt.
func TestFsyncTruncateFailClosed(t *testing.T) {
	h := NewStrictHandler("v")
	if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	// Strict WriteFile itself is EIO (already covered); seed data via a
	// working syncer so Truncate/Fsync have content to protect.
	h.SetSyncer(NoopSyncer{})
	if _, e := h.WriteFile("f", 0, []byte("hello")); e != 0 {
		t.Fatal(e)
	}
	// Drop the coordinator: subsequent durability calls must fail closed.
	h.SetSyncer(nil)
	if e := h.Fsync("f"); e != syscall.EIO {
		t.Fatalf("Fsync without coordinator must be EIO, got %v", e)
	}
	if e := h.Truncate("f", 3); e != syscall.EIO {
		t.Fatalf("Truncate without coordinator must be EIO, got %v", e)
	}
	if got, _ := h.ReadFile("f"); string(got) != "hello" {
		t.Fatalf("failed Truncate must roll back, got %q", got)
	}
	if e := h.Mkdir("d", 0755); e != 0 {
		t.Fatal(e)
	}
	if e := h.FsyncDir("d"); e != syscall.EIO {
		t.Fatalf("FsyncDir without coordinator must be EIO, got %v", e)
	}
	// ENOENT still takes precedence over EIO.
	if e := h.Fsync("missing"); e != syscall.ENOENT {
		t.Fatalf("Fsync missing must be ENOENT, got %v", e)
	}
}

// TestFsyncTruncateCoordinatorError verifies a failing coordinator surfaces as
// EIO and rolls back (no silent success).
func TestFsyncTruncateCoordinatorError(t *testing.T) {
	h := NewStrictHandler("v")
	h.SetSyncer(NoopSyncer{})
	if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	if _, e := h.WriteFile("f", 0, []byte("hello")); e != 0 {
		t.Fatal(e)
	}
	h.SetSyncer(&failingSyncer{err: errors.New("quorum lost")})
	if e := h.Fsync("f"); e != syscall.EIO {
		t.Fatalf("Fsync coordinator error must be EIO, got %v", e)
	}
	if e := h.Truncate("f", 2); e != syscall.EIO {
		t.Fatalf("Truncate coordinator error must be EIO, got %v", e)
	}
	if got, _ := h.ReadFile("f"); string(got) != "hello" {
		t.Fatalf("failed Truncate must roll back, got %q", got)
	}
	if e := h.FsyncDir("missing"); e != syscall.ENOENT {
		t.Fatalf("FsyncDir missing must be ENOENT, got %v", e)
	}
}

// TestHandleFsyncFailClosed verifies a kernel handle cannot report durable
// success without a coordinator.
func TestHandleFsyncFailClosed(t *testing.T) {
	h := NewStrictHandler("v")
	r := &Root{h: h}
	ctx := context.Background()
	fh, _, errno := r.Open(ctx, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("open: %v", errno)
	}
	if e := r.Fsync(ctx, fh, 0); e != syscall.EIO {
		t.Fatalf("handle fsync without coordinator must be EIO, got %v", e)
	}
	// Working coordinator succeeds.
	h.SetSyncer(NoopSyncer{})
	fh2, _, _ := r.Open(ctx, syscall.O_RDWR)
	if e := r.Fsync(ctx, fh2, 0); e != 0 {
		t.Fatalf("handle fsync with coordinator: %v", e)
	}
}
