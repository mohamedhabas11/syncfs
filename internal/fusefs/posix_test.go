package fusefs

import (
	"syscall"
	"testing"
)

func TestCompatProfileSurface(t *testing.T) {
	h := NewHandler("compat")
	// create + write + read + truncate
	if _, e := h.CreateFile("a", 0644, 0, 0); e != 0 {
		t.Fatalf("create: %v", e)
	}
	if _, e := h.CreateFile("a", 0644, 0, 0); e != syscall.EEXIST {
		t.Fatalf("O_EXCL must fail EEXIST, got %v", e)
	}
	if n, e := h.WriteFile("a", 0, []byte("hello")); e != 0 || n != 5 {
		t.Fatalf("write: %v %d", e, n)
	}
	got, _ := h.ReadFile("a")
	if string(got) != "hello" {
		t.Fatalf("read: %q", got)
	}
	if e := h.Truncate("a", 3); e != 0 {
		t.Fatal(e)
	}
	got, _ = h.ReadFile("a")
	if string(got) != "hel" {
		t.Fatalf("after truncate: %q", got)
	}
	// directories
	if e := h.Mkdir("d", 0755); e != 0 {
		t.Fatal(e)
	}
	if e := h.Rmdir("d"); e != 0 {
		t.Fatal(e)
	}
	// symlink
	if e := h.Symlink("l", "a"); e != 0 {
		t.Fatal(e)
	}
	// rename (atomic move)
	if e := h.Rename("a", "b"); e != 0 {
		t.Fatal(e)
	}
	if _, e := h.ReadFile("a"); e != syscall.ENOENT {
		t.Fatalf("old name must be gone, got %v", e)
	}
	if got, _ := h.ReadFile("b"); string(got) != "hel" {
		t.Fatalf("renamed: %q", got)
	}
	// rmdir non-empty fails
	_ = h.Mkdir("x", 0755)
	// Fsync ok + notfound
	if e := h.Fsync("x"); e != 0 {
		t.Fatal(e)
	}
	if e := h.Fsync("missing"); e != syscall.ENOENT {
		t.Fatalf("fsync missing: %v", e)
	}
	// unlink + put
	if e := h.Unlink("l"); e != 0 {
		t.Fatal(e)
	}
	names, _ := h.Names()
	t.Logf("names: %v", names)
}
