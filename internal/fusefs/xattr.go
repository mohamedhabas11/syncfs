// Xattr surface for M5.x compatibility line: user.* namespaces live;
// everything else stays EOPNOTSUPP (exactly as advertised in the matrix).
package fusefs

import (
	"strings"
	"syscall"
	"time"
)

// Syncer is the coordinator seam M5 wires through the volume coordinator.
// Every mutating Handler path must route through this seam before it may
// return success: WriteFile via OnWrite, Truncate via OnTruncate, and every
// fsync/fsyncdir via OnFsync/OnFsyncDir. A nil syncer (strict handler with no
// coordinator wired) must fail closed with EIO — never acknowledge.
type Syncer interface {
	OnWrite(name string, at int64, data []byte) error
	// OnTruncate must durably commit a size change before success.
	OnTruncate(name string, size uint64) error
	// OnFsync must wait for the durable quorum commit of name before success.
	OnFsync(name string) error
	// OnFsyncDir must wait for namespace-command durability of name before
	// success. The empty name denotes the root directory (Root handle fsync).
	OnFsyncDir(name string) error
}

// NoopSyncer is the test-double syncer for NewHandler (acknowledges writes).
// It is TEST-ONLY and non-durable by definition. Production mounts must use
// NewStrictHandler plus a real coordinator via SetSyncer; never install this
// in a production mount.
type NoopSyncer struct{}

func (NoopSyncer) OnWrite(name string, at int64, data []byte) error { return nil }
func (NoopSyncer) OnTruncate(name string, size uint64) error        { return nil }
func (NoopSyncer) OnFsync(name string) error                        { return nil }
func (NoopSyncer) OnFsyncDir(name string) error                     { return nil }

// SetSyncer installs the coordinator seam used by mutation paths.
// A nil argument is kept as nil (fail-closed): it does NOT reset to
// NoopSyncer. Passing nil explicitly leaves the handler without a
// coordinator, so writes/fsyncs return EIO as for NewStrictHandler.
func (h *Handler) SetSyncer(s Syncer) {
	h.syncer = s
}

// xattrName validates a name: only user.* with a non-empty suffix is
// supported (bare "user." and NUL-bearing keys are rejected).
func xattrName(name string) (string, syscall.Errno) {
	if strings.Contains(name, "\x00") {
		return "", syscall.EINVAL
	}
	if strings.HasPrefix(name, "user.") {
		if len(name) <= len("user.") {
			return "", syscall.EINVAL
		}
		return name, syscall.Errno(0)
	}
	if name == "security.selinux" {
		return "", syscall.EPERM
	}
	return "", syscall.EOPNOTSUPP
}

// SetXattr sets a user.* attr on an inode.
func (h *Handler) SetXattr(name string, key string, value []byte) syscall.Errno {
	if _, ne := xattrName(key); ne != 0 {
		return ne
	}
	if len(value) > 64<<10 {
		return syscall.EFBIG
	}
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return syscall.ENOENT
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	if ns.xattrs == nil {
		ns.xattrs = map[string][]byte{}
	}
	ns.xattrs[key] = append([]byte(nil), value...)
	ns.mtime = time.Now()
	return syscall.Errno(0)
}

// GetXattr reads a user.* attr.
func (h *Handler) GetXattr(name string, key string) ([]byte, syscall.Errno) {
	if _, ne := xattrName(key); ne != 0 {
		return nil, ne
	}
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return nil, syscall.ENOENT
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	v, ok := ns.xattrs[key]
	if !ok {
		return nil, syscall.ENODATA
	}
	return append([]byte(nil), v...), syscall.Errno(0)
}
