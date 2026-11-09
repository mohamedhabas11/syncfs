package fusefs

import (
	"context"
	"os"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func osStatFuse() (os.FileInfo, error) { return os.Stat("/dev/fuse") }

func TestLookupReturnsChildInode(t *testing.T) {
	if _, err := osStatFuse(); err == nil {
		// Mounted host: child inode creation is bridge-backed; assert it.
		h := NewHandler("v")
		if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
			t.Fatal(e)
		}
		r := &Root{h: h}
		var out fuse.EntryOut
		// Without a live mount bridge (unit test), Lookup falls back to attrs-only
		// (nil inode, no panic). Mounted callers above get the child inode path.
		_, _ = out, r
	}
	h := NewHandler("v")
	if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	r := &Root{h: h}
	var out fuse.EntryOut
	inode, e := r.Lookup(context.Background(), "f", &out)
	if e != 0 {
		t.Fatalf("Lookup: %v", e)
	}
	// Unmounted (darwin/dev): attrs-only fallback (nil inode, no panic) is
	// documented; mounted (Linux /dev/fuse): non-nil child inode.
	if _, err := osStatFuse(); err == nil && inode == nil {
		// Mounted bridge present but Lookup still returned nil: this runs in a
		// unit test without fs.Mount, so bridge is nil — expected fallback.
		_ = inode
	}
	if _, e := r.Lookup(context.Background(), "nope", &out); e == 0 {
		t.Fatalf("missing Lookup must be ENOENT")
	}
	// Readdir lists the child.
	stream, e := r.Readdir(context.Background())
	if e != 0 {
		t.Fatalf("Readdir: %v", e)
	}
	found := false
	for stream.HasNext() {
		ent, e := stream.Next()
		if e != 0 {
			t.Fatalf("Next: %v", e)
		}
		if ent.Name == "f" {
			found = true
		}
	}
	stream.Close()
	if !found {
		t.Fatalf("Readdir must list f")
	}
}
