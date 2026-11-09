package fusefs

import (
	"syscall"
	"testing"
)

type fakeSyncer struct{ writes int }

func (f *fakeSyncer) OnWrite(name string, at int64, data []byte) error {
	f.writes++
	return nil
}
func (f *fakeSyncer) OnTruncate(name string, size uint64) error { return nil }
func (f *fakeSyncer) OnFsync(name string) error                 { return nil }
func (f *fakeSyncer) OnFsyncDir(name string) error              { return nil }

func TestXattrUserNamespaceOnly(t *testing.T) {
	h := NewHandler("xattr")
	h.CreateFile("f", 0644, 0, 0)
	if e := h.SetXattr("f", "user.trackme", []byte("1")); e != 0 {
		t.Fatalf("user.* set: %v", e)
	}
	v, e := h.GetXattr("f", "user.trackme")
	if e != 0 || string(v) != "1" {
		t.Fatalf("get user.*: %v %q", e, v)
	}
	if e := h.SetXattr("f", "security.selinux", []byte("x")); e != syscall.EPERM {
		t.Fatalf("selinux must be EPERM, got %v", e)
	}
	if e := h.SetXattr("f", "trusted.foo", []byte("x")); e != syscall.EOPNOTSUPP {
		t.Fatalf("trusted must be EOPNOTSUPP, got %v", e)
	}
	if e := h.SetXattr("missing", "user.a", nil); e != syscall.ENOENT {
		t.Fatalf("missing file: %v", e)
	}
	if _, e := h.GetXattr("f", "user.unknown"); e != syscall.ENODATA {
		t.Fatalf("unknown attr: %v", e)
	}
}

func TestSyncerSeamIsCalled(t *testing.T) {
	h := NewHandler("syncer")
	f := &fakeSyncer{}
	h.SetSyncer(f)
	h.CreateFile("g", 0644, 0, 0)
	_, _ = h.WriteFile("g", 0, []byte("abc"))
	if f.writes != 1 {
		t.Fatalf("syncer must be called once, got %d", f.writes)
	}
	// nil is kept as nil (fail-closed), NOT reset to NoopSyncer.
	h.SetSyncer(nil)
	if _, e := h.WriteFile("g", 0, []byte("z")); e != syscall.EIO {
		t.Fatalf("nil syncer must fail closed with EIO, got %v", e)
	}
}
