// POSIX surface for M3-02 (on NodeState, before fs wiring is complete).
// These methods are the compatibility evidence surface; the FUSE adapter
// (fusefs.go) calls them.
package fusefs

import (
	"io"
	"sort"
	"syscall"
	"time"
)

// createNode builds a root-level child of the given kind.
func (h *Handler) createNode(kind int, name string, mode uint32, uid, gid uint32) (*NodeState, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.root.children[name]; dup {
		return nil, syscall.EEXIST
	}
	ns := &NodeState{kind: kind, mode: mode, uid: uid, gid: gid, mtime: time.Now()}
	if kind == KDir {
		ns.children = map[string]*NodeState{}
	}
	h.root.children[name] = ns
	ns.parent = h.root
	return ns, syscall.Errno(0)
}

// CreateFile makes a regular file.
func (h *Handler) CreateFile(name string, mode, uid, gid uint32) (*NodeState, syscall.Errno) {
	return h.createNode(KFile, name, mode, uid, gid)
}

// Mkdir makes a directory.
func (h *Handler) Mkdir(name string, mode uint32) syscall.Errno {
	_, e := h.createNode(KDir, name, mode, 0, 0)
	return e
}

// ReadFile returns the file's bytes (direct-I/O surface).
func (h *Handler) ReadFile(name string) ([]byte, syscall.Errno) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ns, ok := h.root.children[name]
	if !ok {
		return nil, syscall.ENOENT
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	if ns.kind != KFile {
		return nil, syscall.EISDIR
	}
	return append([]byte(nil), ns.data...), syscall.Errno(0)
}

// WriteFile writes at offset, growing as needed (append = write at EOF).
func (h *Handler) WriteFile(name string, at int64, data []byte) (uint32, syscall.Errno) {
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return 0, syscall.ENOENT
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	if ns.kind != KFile {
		return 0, syscall.EISDIR
	}
	if at < 0 {
		return 0, syscall.EINVAL
	}
	end := at + int64(len(data))
	if end > int64(len(ns.data)) {
		ns.data = append(ns.data, make([]byte, end-int64(len(ns.data)))...)
	}
	copy(ns.data[at:end], data)
	ns.mtime = time.Now()
	return uint32(len(data)), syscall.Errno(0)
}

// Truncate sets the file size (zero-fill or extend with zeros).
func (h *Handler) Truncate(name string, size uint64) syscall.Errno {
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return syscall.ENOENT
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	if ns.kind != KFile {
		return syscall.EISDIR
	}
	if size < uint64(len(ns.data)) {
		ns.data = ns.data[:size]
	} else {
		ns.data = append(ns.data, make([]byte, size-uint64(len(ns.data)))...)
	}
	ns.mtime = time.Now()
	return syscall.Errno(0)
}

// Fsync marks data durable (no-op stub; durability comes from coordinator).
func (h *Handler) Fsync(name string) syscall.Errno {
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return syscall.ENOENT
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.mtime = time.Now()
	return syscall.Errno(0)
}

// Names returns sorted names in the root (Readdir surface).
func (h *Handler) Names() ([]string, syscall.Errno) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []string
	for n := range h.root.children {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, syscall.Errno(0)
}

// Rename moves a child within the root atomically.
func (h *Handler) Rename(src, dst string) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	ns, ok := h.root.children[src]
	if !ok {
		return syscall.ENOENT
	}
	delete(h.root.children, src)
	h.root.children[dst] = ns
	return syscall.Errno(0)
}

// Symlink creates a symlink child.
func (h *Handler) Symlink(name, target string) syscall.Errno {
	_, e := h.createNode(KSymlink, name, 0777, 0, 0)
	if e != 0 {
		return syscall.Errno(e)
	}
	h.root.children[name].link = target
	return syscall.Errno(0)
}

// Unlink removes a child (dirs go through Rmdir).
func (h *Handler) Unlink(name string) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	ns, ok := h.root.children[name]
	if !ok {
		return syscall.ENOENT
	}
	if ns.kind == KDir {
		return syscall.EISDIR
	}
	delete(h.root.children, name)
	return syscall.Errno(0)
}

// Rmdir removes an empty dir.
func (h *Handler) Rmdir(name string) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	ns, ok := h.root.children[name]
	if !ok {
		return syscall.ENOENT
	}
	if ns.kind != KDir {
		return syscall.ENOTDIR
	}
	if len(ns.children) > 0 {
		return syscall.ENOTEMPTY
	}
	delete(h.root.children, name)
	return syscall.Errno(0)
}

var _ = io.EOF
