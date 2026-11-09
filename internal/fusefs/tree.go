// Arbitrary-depth tree resolution (T04). NodeState already supports
// per-directory children maps; this file adds path walking so a/b/c works
// in-memory (kernel binding to metadata inode IDs arrives with T03 full
// wiring — see docs/known-limitations.md #15).
package fusefs

import (
	"strings"
	"syscall"
)

// splitPath splits "a/b/c" into [a b c]; "" and "/" and "." all mean root.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return nil
	}
	return strings.Split(p, "/")
}

// walk resolves path under h.root. Caller must hold h.mu (RLock for read,
// Lock for write); walk itself does not lock h.mu but locks NodeState mu.
func (h *Handler) walk(parts []string, forWrite bool) (*NodeState, syscall.Errno) {
	cur := h.root
	for i, part := range parts {
		if e := validName(part); e != 0 {
			return nil, e
		}
		cur.mu.RLock()
		next, ok := cur.children[part]
		isDir := cur.kind == KDir
		cur.mu.RUnlock()
		if !isDir {
			return nil, syscall.ENOTDIR
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if i < len(parts)-1 {
			if next.kind != KDir {
				return nil, syscall.ENOTDIR
			}
		}
		cur = next
	}
	return cur, 0
}

// mkdirPath creates path (all segments). Fails if any parent missing.
func (h *Handler) MkdirPath(path string, mode uint32) syscall.Errno {
	parts := splitPath(path)
	if len(parts) == 0 {
		return syscall.EEXIST
	}
	h.mu.Lock()
	parent, e := h.walk(parts[:len(parts)-1], true)
	if e != 0 {
		h.mu.Unlock()
		return e
	}
	_, e2 := h.createChildLocked(parent, parts[len(parts)-1], KDir, mode, 0, 0)
	h.mu.Unlock()
	return e2
}

// createFilePath creates a file at path.
func (h *Handler) createFilePath(path string, mode, uid, gid uint32) (*NodeState, syscall.Errno) {
	parts := splitPath(path)
	if len(parts) == 0 {
		return nil, syscall.EINVAL
	}
	h.mu.Lock()
	parent, e := h.walk(parts[:len(parts)-1], true)
	h.mu.Unlock()
	if e != 0 {
		return nil, e
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.createChildLocked(parent, parts[len(parts)-1], KFile, mode, uid, gid)
}

func (h *Handler) createChildLocked(parent *NodeState, name string, kind int, mode, uid, gid uint32) (*NodeState, syscall.Errno) {
	if e := validName(name); e != 0 {
		return nil, e
	}
	parent.mu.Lock()
	defer parent.mu.Unlock()
	if parent.kind != KDir {
		return nil, syscall.ENOTDIR
	}
	if parent.children == nil {
		parent.children = map[string]*NodeState{}
	}
	if _, dup := parent.children[name]; dup {
		return nil, syscall.EEXIST
	}
	ns := &NodeState{kind: kind, mode: mode, uid: uid, gid: gid}
	if kind == KDir {
		ns.children = map[string]*NodeState{}
	}
	parent.children[name] = ns
	ns.parent = parent
	return ns, 0
}
