package fusefs

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// TestStrictTimeoutsZero verifies the strict profile: zero metadata cache TTLs.
func TestStrictTimeoutsZero(t *testing.T) {
	entry, attr, negative := StrictTimeouts()
	if entry != 0 || attr != 0 || negative != 0 {
		t.Fatalf("strict TTLs must be zero (no stale cache), got %v %v %v", entry, attr, negative)
	}
	opts := FSOptions()
	if opts.AllowOther {
		t.Fatalf("strict mount must not AllowOther")
	}
	if opts.EntryTimeout == nil || *opts.EntryTimeout != 0 {
		t.Fatalf("FSOptions entry timeout must be zero")
	}
	if opts.AttrTimeout == nil || *opts.AttrTimeout != 0 {
		t.Fatalf("FSOptions attr timeout must be zero")
	}
	if opts.NegativeTimeout == nil || *opts.NegativeTimeout != 0 {
		t.Fatalf("FSOptions negative timeout must be zero")
	}
}

// TestCallerCredentialsEnforcedInLookup verifies that fuse.FromContext
// caller credentials actually gate Lookup (not discarded).
func TestCallerCredentialsEnforcedInLookup(t *testing.T) {
	h := NewHandler("cred")
	// file owned by uid 1000 mode 0600: only owner + root may read.
	if _, e := h.CreateFile("secret", 0600, 1000, 1000); e != 0 {
		t.Fatal(e)
	}
	r := &Root{h: h}
	mkctx := func(uid, gid uint32) context.Context {
		return fuse.NewContext(context.Background(), &fuse.Caller{Owner: fuse.Owner{Uid: uid, Gid: gid}, Pid: 1})
	}
	// owner can lookup
	var out fuse.EntryOut
	if _, e := r.Lookup(mkctx(1000, 1000), "secret", &out); e != 0 {
		t.Fatalf("owner lookup: %v", e)
	}
	// other uid is denied
	if _, e := r.Lookup(mkctx(1001, 1001), "secret", &out); e != syscall.EACCES {
		t.Fatalf("other lookup must be EACCES, got %v", e)
	}
	// root bypasses
	if _, e := r.Lookup(mkctx(0, 0), "secret", &out); e != 0 {
		t.Fatalf("root lookup: %v", e)
	}
	// missing name is ENOENT (after parent-exec check for an allowed user)
	if _, e := r.Lookup(mkctx(1000, 1000), "missing", &out); e != syscall.ENOENT {
		t.Fatalf("missing must be ENOENT, got %v", e)
	}
}

type failingSyncer struct{ err error }

func (f *failingSyncer) OnWrite(name string, at int64, data []byte) error { return f.err }

// TestWriteFileFailsClosedOnSyncerError verifies writes are rolled back and
// EIO returned when the coordinator seam fails (fail closed).
func TestWriteFileFailsClosedOnSyncerError(t *testing.T) {
	h := NewHandler("failclosed")
	h.SetSyncer(&failingSyncer{err: errors.New("quorum lost")})
	if _, e := h.CreateFile("f", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	if _, e := h.WriteFile("f", 0, []byte("data")); e != syscall.EIO {
		t.Fatalf("syncer error must be EIO, got %v", e)
	}
	got, _ := h.ReadFile("f")
	if len(got) != 0 {
		t.Fatalf("failed write must roll back, got %q", got)
	}
	// healthy syncer succeeds
	h.SetSyncer(NoopSyncer{})
	if _, e := h.WriteFile("f", 0, []byte("ok")); e != 0 {
		t.Fatalf("healthy write: %v", e)
	}
	got, _ = h.ReadFile("f")
	if string(got) != "ok" {
		t.Fatalf("read back: %q", got)
	}
}

// TestOpenReleaseFsyncLifecycle verifies direct-I/O handles: Open returns
// FOPEN_DIRECT_IO, Release is idempotent, released Fsync fails closed.
func TestOpenReleaseFsyncLifecycle(t *testing.T) {
	h := NewHandler("lifecycle")
	r := &Root{h: h}
	ctx := context.Background()
	fh, flags, errno := r.Open(ctx, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("open: %v", errno)
	}
	if flags&fuse.FOPEN_DIRECT_IO == 0 {
		t.Fatalf("mutable handles must be direct-I/O, flags=%d", flags)
	}
	// fsync via Root delegates to the handle
	if e := r.Fsync(ctx, fh, 0); e != 0 {
		t.Fatalf("fsync open handle: %v", e)
	}
	// flush then release; second release is still success (idempotent)
	if e := r.Flush(ctx, fh); e != 0 {
		t.Fatalf("flush: %v", e)
	}
	if e := r.Release(ctx, fh); e != 0 {
		t.Fatalf("release: %v", e)
	}
	if e := r.Release(ctx, fh); e != 0 {
		t.Fatalf("second release must be idempotent: %v", e)
	}
	// fsync after release is EBADF (fail closed)
	if e := r.Fsync(ctx, fh, 0); e != syscall.EBADF {
		t.Fatalf("fsync after release must be EBADF, got %v", e)
	}
	if _, _, e := r.Fsync(ctx, fh, 0), 0, syscall.Errno(0); e != 0 {
		t.Fatalf("unreachable: %v", e)
	}
	_ = fs.FileHandle(nil)
}

// TestHardLinkSymlinkFsyncDir verifies the M3-02 additions.
func TestHardLinkSymlinkFsyncDir(t *testing.T) {
	h := NewHandler("posix2")
	if _, e := h.CreateFile("a", 0644, 0, 0); e != 0 {
		t.Fatal(e)
	}
	if _, e := h.WriteFile("a", 0, []byte("x")); e != 0 {
		t.Fatal(e)
	}
	if e := h.Link("a", "b"); e != 0 {
		t.Fatalf("link: %v", e)
	}
	if got, _ := h.ReadFile("b"); string(got) != "x" {
		t.Fatalf("hard link must share inode: %q", got)
	}
	if e := h.Link("a", "b"); e != syscall.EEXIST {
		t.Fatalf("duplicate link must be EEXIST, got %v", e)
	}
	if e := h.Mkdir("d", 0755); e != 0 {
		t.Fatal(e)
	}
	if e := h.Link("d", "e"); e != syscall.EPERM {
		t.Fatalf("link dir must be EPERM, got %v", e)
	}
	if e := h.Symlink("l", "a"); e != 0 {
		t.Fatal(e)
	}
	if target, e := h.Readlink("l"); e != 0 || target != "a" {
		t.Fatalf("readlink: %q %v", target, e)
	}
	if e := h.FsyncDir("d"); e != 0 {
		t.Fatalf("fsyncdir: %v", e)
	}
	if e := h.FsyncDir("a"); e != syscall.ENOTDIR {
		t.Fatalf("fsyncdir on file must be ENOTDIR, got %v", e)
	}
	// invalid names fail closed
	if _, e := h.CreateFile("../evil", 0644, 0, 0); e != syscall.EINVAL {
		t.Fatalf("dotdot name must be EINVAL, got %v", e)
	}
	if _, e := h.CreateFile("a/b", 0644, 0, 0); e != syscall.EINVAL {
		t.Fatalf("slash name must be EINVAL, got %v", e)
	}
	if e := h.Rename("a", "a/b"); e != syscall.EINVAL {
		t.Fatalf("rename to slash must be EINVAL, got %v", e)
	}
}
