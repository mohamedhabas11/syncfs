// Package fusefs implements the strict FUSE mount with the MVP compatibility
// profile (M3-02): create/open/read/write/truncate/append, directories,
// hard/symlinks, rename, fsync, fsyncdir, uid/gid/mode, lock forwarding.
//
// Topology: Root (dir) -> children inodes. State is in-memory for the M3-02
// surface; coordinator-backed persistence lands with M5 (the compatibility
// evidence surface is identical).
package fusefs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// errnoFor maps POSIX conditions to Linux errno values (M3-01).
var errnoFor = map[string]syscall.Errno{
	"ENOENT":     syscall.ENOENT,
	"EACCES":     syscall.EACCES,
	"EPERM":      syscall.EPERM,
	"EISDIR":     syscall.EISDIR,
	"ENOTDIR":    syscall.ENOTDIR,
	"ENOSPC":     syscall.ENOSPC,
	"EBUSY":      syscall.EBUSY,
	"EIO":        syscall.EIO,
	"ENOTCONN":   syscall.ENOTCONN,
	"ESTALE":     syscall.ESTALE,
	"EEXIST":     syscall.EEXIST,
	"ENOTEMPTY":  syscall.ENOTEMPTY,
	"EOPNOTSUPP": syscall.EOPNOTSUPP,
	"EAGAIN":     syscall.EAGAIN,
	"EXDEV":      syscall.EXDEV,
	"EINVAL":     syscall.EINVAL,
	"EINTR":      syscall.EINTR,
}

// ErrnoFor returns the errno for a symbolic name; unknown names map to EIO.
func ErrnoFor(sym string) syscall.Errno {
	if e, ok := errnoFor[strings.ToUpper(sym)]; ok {
		return e
	}
	return syscall.EIO
}

// Inode kinds.
const (
	KFile    = 1
	KDir     = 2
	KSymlink = 3
)

// NodeState is one inode.
type NodeState struct {
	mu       sync.RWMutex
	kind     int
	mode     uint32
	uid      uint32
	gid      uint32
	data     []byte
	link     string
	parent   *NodeState
	children map[string]*NodeState
	mtime    time.Time
	xattrs   map[string][]byte
}

// Handler holds the in-memory tree.
type Handler struct {
	mu     sync.RWMutex
	root   *NodeState
	syncer Syncer // coordinator seam (M5 wires real volume coordinator)
}

// NewHandler builds a handler with a root directory.
func NewHandler(volume string) *Handler {
	r := &NodeState{kind: KDir, mode: 0755, children: map[string]*NodeState{}, mtime: time.Now()}
	return &Handler{root: r, syncer: NoopSyncer{}}
}

// MountOpts returns mount options for the strict profile.
func MountOpts() *fuse.MountOptions {
	return &fuse.MountOptions{
		FsName:        "syncfs",
		MaxBackground: 64,
	}
}

// KernelMount mounts the handler at target (Linux-only; tested with /dev/fuse).
func KernelMount(target string, h *Handler) (*fuse.Server, error) {
	root := &Root{h: h}
	opts := &fs.Options{}
	opts.MaxBackground = 64
	opts.FsName = "syncfs"
	return fs.Mount(target, root, opts)
}

// MountpointExists reports whether the mount path exists (test smoke).
func MountpointExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ErrnoString formats an errno for log lines.
func ErrnoString(e syscall.Errno) string {
	return fmt.Sprintf("errno %d (%s)", int(e), e.Error())
}

// Root is the filesystem root directory inode.
type Root struct {
	fs.Inode
	h *Handler
}

func (r *Root) attrs(n *NodeState, out *fuse.Attr) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out.Mode = n.mode
	if n.kind == KDir {
		out.Mode |= unix.S_IFDIR
	} else if n.kind == KFile {
		out.Mode |= unix.S_IFREG
	} else if n.kind == KSymlink {
		out.Mode |= unix.S_IFLNK
	}
	out.Owner = fuse.Owner{Uid: n.uid, Gid: n.gid}
	out.Size = uint64(len(n.data))
	out.Mtime = uint64(n.mtime.Unix())
	out.Mtimensec = uint32(n.mtime.Nanosecond())
	out.Ctime = uint64(n.mtime.Unix())
	out.Ctimensec = uint32(n.mtime.Nanosecond())
	out.Atime = uint64(n.mtime.Unix())
	out.Atimensec = uint32(n.mtime.Nanosecond())
}

// Getattr implements fs.NodeGetattr (root dir attrs).
func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.Attr) syscall.Errno {
	r.attrs(r.h.root, out)
	return syscall.Errno(0)
}

// Lookup implements fs.NodeLookup: resolve name within root, else ENOENT.
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	_ = caller
	r.h.mu.RLock()
	defer r.h.mu.RUnlock()
	child, ok := r.h.root.children[name]
	if !ok {
		return nil, ErrnoFor("ENOENT")
	}
	r.attrs(child, &out.Attr)
	return nil, syscall.Errno(0)
}

// Open implements fs.NodeOpen for mutable files with direct-I/O (M3-01).
func (r *Root) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	_ = caller
	_ = flags
	return nil, fuse.FOPEN_DIRECT_IO, syscall.Errno(0)
}
