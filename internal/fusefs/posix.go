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
	if e := validName(name); e != 0 {
		return nil, e
	}
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
// Fail-closed: if the coordinator seam errors, the write is rolled back and
// EIO is returned (never a false durable receipt).
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
	if at < 0 || at > 1<<62 {
		return 0, syscall.EINVAL
	}
	if int64(len(data)) > 1<<62-at {
		return 0, syscall.EFBIG
	}
	prevData := append([]byte(nil), ns.data...)
	prevMtime := ns.mtime
	end := at + int64(len(data))
	if uint64(end) > MaxFileBytes {
		return 0, syscall.EFBIG
	}
	if end > int64(len(ns.data)) {
		ns.data = append(ns.data, make([]byte, end-int64(len(ns.data)))...)
	}
	copy(ns.data[at:end], data)
	ns.mtime = time.Now()
	if h.syncer == nil {
		h.syncer = NoopSyncer{}
	}
	if err := h.syncer.OnWrite(name, at, append([]byte(nil), data...)); err != nil {
		ns.data = prevData
		ns.mtime = prevMtime
		return 0, syscall.EIO
	}
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
	if size > MaxFileBytes {
		return syscall.EFBIG
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

// MaxFileBytes caps in-memory growth for the FUSE handler surface (quota gate).
const MaxFileBytes = 16 << 20 // 16 MiB

// validName rejects empty, dot, slash/NUL-bearing, and over-long names
// (fail closed). Over-long names return ENAMETOOLONG.
func validName(name string) syscall.Errno {
	if name == "" || name == "." || name == ".." {
		return syscall.EINVAL
	}
	if len(name) > 255 {
		return syscall.ENAMETOOLONG
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' || name[i] == 0 {
			return syscall.EINVAL
		}
	}
	return syscall.Errno(0)
}

// Rename moves a child within the root atomically.
func (h *Handler) Rename(src, dst string) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := validName(src); e != 0 {
		return e
	}
	if e := validName(dst); e != 0 {
		return e
	}
	ns, ok := h.root.children[src]
	if !ok {
		return syscall.ENOENT
	}
	if old, hit := h.root.children[dst]; hit && old != ns {
		// POSIX type checks: file onto dir -> EISDIR, dir onto file -> ENOTDIR,
		// non-empty dir onto dir -> ENOTEMPTY. Plainer overwrites replace.
		if ns.kind != KDir && old.kind == KDir {
			return syscall.EISDIR
		}
		if ns.kind == KDir && old.kind != KDir {
			return syscall.ENOTDIR
		}
		if ns.kind == KDir && old.kind == KDir && len(old.children) > 0 {
			return syscall.ENOTEMPTY
		}
	}
	delete(h.root.children, src)
	h.root.children[dst] = ns
	return syscall.Errno(0)
}

// Link creates a hard link: a second name for the same inode. Directories
// cannot be hard-linked (EPERM per POSIX).
func (h *Handler) Link(existing, newName string) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	ns, ok := h.root.children[existing]
	if !ok {
		return syscall.ENOENT
	}
	if ns.kind == KDir {
		return syscall.EPERM
	}
	if _, dup := h.root.children[newName]; dup {
		return syscall.EEXIST
	}
	if e := validName(newName); e != 0 {
		return e
	}
	h.root.children[newName] = ns
	return syscall.Errno(0)
}

// Readlink returns the symlink target.
func (h *Handler) Readlink(name string) (string, syscall.Errno) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ns, ok := h.root.children[name]
	if !ok {
		return "", syscall.ENOENT
	}
	if ns.kind != KSymlink {
		return "", syscall.EINVAL
	}
	return ns.link, syscall.Errno(0)
}

// FsyncDir marks a directory durable (coordinator seam for M5). Not-found
// returns ENOENT; files return ENOTDIR.
func (h *Handler) FsyncDir(name string) syscall.Errno {
	h.mu.RLock()
	ns, ok := h.root.children[name]
	h.mu.RUnlock()
	if !ok {
		return syscall.ENOENT
	}
	if ns.kind != KDir {
		return syscall.ENOTDIR
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.mtime = time.Now()
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
