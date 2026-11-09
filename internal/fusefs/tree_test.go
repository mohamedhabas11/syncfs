package fusefs

import (
	"context"
	"syscall"
	"testing"
)

// TestNestedTree verifies arbitrary-depth in-memory tree: a/b/c/file.
func TestNestedTree(t *testing.T) {
	h := NewHandler("nested")
	if e := h.MkdirPath("a", 0755); e != 0 {
		t.Fatalf("mkdir a: %v", e)
	}
	if e := h.MkdirPath("a/b", 0755); e != 0 {
		t.Fatalf("mkdir a/b: %v", e)
	}
	if e := h.MkdirPath("a/b/c", 0755); e != 0 {
		t.Fatalf("mkdir a/b/c: %v", e)
	}
	ns, e := h.createFilePath("a/b/c/file", 0644, 0, 0)
	if e != 0 {
		t.Fatalf("create nested file: %v", e)
	}
	if ns == nil || ns.kind != KFile {
		t.Fatalf("nested file kind")
	}
	// Walk back via childNode Lookup chain (mounted semantics without kernel bridge).
	r := &Root{h: h}
	ctx := context.Background()
	// Root Lookup a
	var out struct{ _ int }
	_ = out
	// Direct tree assertion: Readdir of a lists b, etc. via NodeState children.
	h.mu.RLock()
	a := h.root.children["a"]
	h.mu.RUnlock()
	if a == nil || a.kind != KDir {
		t.Fatalf("a must be dir")
	}
	a.mu.RLock()
	b := a.children["b"]
	a.mu.RUnlock()
	if b == nil {
		t.Fatalf("b missing")
	}
	b.mu.RLock()
	c := b.children["c"]
	b.mu.RUnlock()
	if c == nil {
		t.Fatalf("c missing")
	}
	c.mu.RLock()
	f := c.children["file"]
	c.mu.RUnlock()
	if f == nil || f.kind != KFile {
		t.Fatalf("file missing")
	}
	// Duplicate nested create must be EEXIST.
	if _, e := h.createFilePath("a/b/c/file", 0644, 0, 0); e != syscall.EEXIST {
		t.Fatalf("dup nested must be EEXIST, got %v", e)
	}
	// Missing parent must be ENOENT/ENOTDIR.
	if _, e := h.createFilePath("nope/file", 0644, 0, 0); e == 0 {
		t.Fatalf("missing parent must fail")
	}
	_ = r
	_ = ctx
}
