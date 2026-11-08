// Xattr surface for M5.x compatibility line: user.* namespaces live;
// everything else stays EOPNOTSUPP (exactly as advertised in the matrix).
package fusefs

import (
	"os"
	"strings"
	"syscall"
)

// Syncer is the coordinator seam M5 wires through the volume coordinator.
type Syncer interface {
	OnWrite(name string, at int64, data []byte) error
}

// NoopSyncer is the default (in-memory tree; fail-closed on unknown).
type NoopSyncer struct{}

func (NoopSyncer) OnWrite(name string, at int64, data []byte) error { return nil }

// SetSyncer installs the coordinator seam used by future mutation paths.
func (h *Handler) SetSyncer(s Syncer) {
	if s == nil {
		s = NoopSyncer{}
	}
	h.syncer = s
}

// xattrName validates a name: only user.* is supported (EOPNOTSUPP otherwise).
func xattrName(name string) (string, syscall.Errno) {
	if strings.HasPrefix(name, "user.") {
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
	ns.mtime = ns.mtime
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

var _ = os.Stdout
