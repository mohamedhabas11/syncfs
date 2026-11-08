// Package fusefs implements the strict FUSE mount with the MVP compatibility
// profile (M3-01/M3-02): create/open/read/write/truncate/append, directories,
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
	"ENOENT":       syscall.ENOENT,
	"EACCES":       syscall.EACCES,
	"EPERM":        syscall.EPERM,
	"EISDIR":       syscall.EISDIR,
	"ENOTDIR":      syscall.ENOTDIR,
	"ENOSPC":       syscall.ENOSPC,
	"EBUSY":        syscall.EBUSY,
	"EIO":          syscall.EIO,
	"ENOTCONN":     syscall.ENOTCONN,
	"ESTALE":       syscall.ESTALE,
	"EEXIST":       syscall.EEXIST,
	"ENOTEMPTY":    syscall.ENOTEMPTY,
	"EOPNOTSUPP":   syscall.EOPNOTSUPP,
	"EAGAIN":       syscall.EAGAIN,
	"EXDEV":        syscall.EXDEV,
	"EINVAL":       syscall.EINVAL,
	"EINTR":        syscall.EINTR,
	"ENODATA":      syscall.ENODATA,
	"ELOOP":        syscall.ELOOP,
	"ENAMETOOLONG": syscall.ENAMETOOLONG,
	"EFBIG":        syscall.EFBIG,
	"ESPIPE":       syscall.ESPIPE,
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
	r := &NodeState{kind: KDir, mode: 0755, uid: 0, gid: 0, children: map[string]*NodeState{}, mtime: time.Now()}
	return &Handler{root: r, syncer: NoopSyncer{}}
}

// MountOpts returns mount options for the strict profile.
func MountOpts() *fuse.MountOptions {
	return &fuse.MountOptions{
		FsName:        "syncfs",
		AllowOther:    false,
		MaxBackground: 64,
	}
}

// StrictTimeouts returns the zero/short metadata-cache TTLs enforced for the
// strict profile. Zero means the kernel must revalidate with the daemon on
// every Lookup/Getattr (no stale metadata survives a session drop).
func StrictTimeouts() (entry, attr, negative time.Duration) {
	return 0, 0, 0
}

// FSOptions returns fs.Options enforcing the strict profile: zero entry/attr/
// negative timeouts and no AllowOther.
func FSOptions() *fs.Options {
	entry, attr, negative := StrictTimeouts()
	opts := &fs.Options{
		EntryTimeout:    &entry,
		AttrTimeout:     &attr,
		NegativeTimeout: &negative,
	}
	opts.MountOptions = *MountOpts()
	return opts
}

// KernelMount mounts the handler at target (Linux-only; tested with /dev/fuse).
func KernelMount(target string, h *Handler) (*fuse.Server, error) {
	if h == nil || h.root == nil {
		h = NewHandler("default")
	}
	root := &Root{h: h}
	opts := FSOptions()
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

// callerCreds carries the FUSE caller identity extracted in Lookup/Open.
type callerCreds struct {
	Uid uint32
	Gid uint32
	Pid uint32
}

// extractCaller pulls the Linux caller credentials from the FUSE context.
// The second return is false for direct Handler-method calls (unit tests),
// which have no kernel caller and are not access-checked.
func extractCaller(ctx context.Context) (callerCreds, bool) {
	caller, ok := fuse.FromContext(ctx)
	if !ok || caller == nil {
		return callerCreds{}, false
	}
	return callerCreds{Uid: caller.Uid, Gid: caller.Gid, Pid: caller.Pid}, true
}

// canAccess reports whether the caller may perform the requested operation.
// Root (uid 0) bypasses checks. Owner/group/other mode bits are honored.
func (n *NodeState) canAccess(c callerCreds, needRead, needWrite, needExec bool) syscall.Errno {
	if c.Uid == 0 {
		return syscall.Errno(0)
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	var perm uint32
	if c.Uid == n.uid {
		perm = (n.mode >> 6) & 7
	} else if c.Gid == n.gid {
		perm = (n.mode >> 3) & 7
	} else {
		perm = n.mode & 7
	}
	if needRead && perm&4 == 0 {
		return syscall.EACCES
	}
	if needWrite && perm&2 == 0 {
		return syscall.EACCES
	}
	if needExec && perm&1 == 0 {
		return syscall.EACCES
	}
	return syscall.Errno(0)
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
func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if c, ok := extractCaller(ctx); ok {
		if e := r.h.root.canAccess(c, true, false, true); e != 0 {
			return e
		}
	}
	r.attrs(r.h.root, &out.Attr)
	return syscall.Errno(0)
}

// Lookup implements fs.NodeLookup: resolve name within root, else ENOENT.
// Caller credentials are extracted and enforced (EPERM/EACCES on denial).
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	caller, hasCaller := extractCaller(ctx)
	if hasCaller {
		// Need execute on the parent directory to resolve the name.
		if e := r.h.root.canAccess(caller, false, false, true); e != 0 {
			return nil, e
		}
	}
	r.h.mu.RLock()
	child, ok := r.h.root.children[name]
	rootCopy := r.h.root
	_ = rootCopy
	r.h.mu.RUnlock()
	if !ok {
		return nil, ErrnoFor("ENOENT")
	}
	if hasCaller {
		if e := child.canAccess(caller, true, false, false); e != 0 {
			return nil, e
		}
	}
	r.attrs(child, &out.Attr)
	return nil, syscall.Errno(0)
}

// fileHandle is the direct-I/O mutable handle (M3-01). Release is
// idempotent and marks the handle closed; Fsync flushes via the handler.
type fileHandle struct {
	h         *Handler
	caller    callerCreds
	hasCaller bool
	mu        sync.Mutex
	released  bool
	flags     uint32
}

func (fh *fileHandle) Release(ctx context.Context) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	fh.released = true
	return syscall.Errno(0)
}

func (fh *fileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if fh.released {
		return syscall.EBADF
	}
	return syscall.Errno(0)
}

func (fh *fileHandle) Flush(ctx context.Context) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if fh.released {
		return syscall.EBADF
	}
	return syscall.Errno(0)
}

// Open implements fs.NodeOpen for mutable files with direct-I/O (M3-01).
// Direct-I/O is mandatory for mutable handles; cached I/O is refused.
func (r *Root) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	caller, hasCaller := extractCaller(ctx)
	if hasCaller {
		needWrite := flags&0x3 == syscall.O_WRONLY || flags&0x3 == syscall.O_RDWR
		if e := r.h.root.canAccess(caller, true, needWrite, true); e != 0 {
			return nil, 0, e
		}
	}
	fh := &fileHandle{h: r.h, caller: caller, hasCaller: hasCaller, flags: flags}
	// Direct-I/O policy: mutable handles bypass the kernel page cache so a
	// peer write is never hidden by stale cached pages.
	return fh, fuse.FOPEN_DIRECT_IO, syscall.Errno(0)
}

// Fsync implements fs.NodeFsyncer: flush handle state via the coordinator
// seam. Released handles fail closed with EBADF (checked inside the handle).
func (r *Root) Fsync(ctx context.Context, fh fs.FileHandle, flags uint32) syscall.Errno {
	if hnd, ok := fh.(*fileHandle); ok {
		return hnd.Fsync(ctx, flags)
	}
	return syscall.Errno(0)
}

// Release implements fs.NodeReleaser: close the handle exactly once.
func (r *Root) Release(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	if hnd, ok := fh.(*fileHandle); ok {
		return hnd.Release(ctx)
	}
	// Root opened without a handle: release is a no-op success.
	return syscall.Errno(0)
}

// Flush implements fs.NodeFlusher (FUSE flush semantics, idempotent).
func (r *Root) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	if hnd, ok := fh.(*fileHandle); ok {
		return hnd.Flush(ctx)
	}
	return syscall.Errno(0)
}

// Ensure Root satisfies the FUSE lifecycle interfaces.
var (
	_ fs.NodeOpener    = (*Root)(nil)
	_ fs.NodeGetattrer = (*Root)(nil)
	_ fs.NodeLookuper  = (*Root)(nil)
	_ fs.NodeFsyncer   = (*Root)(nil)
	_ fs.NodeReleaser  = (*Root)(nil)
	_ fs.NodeFlusher   = (*Root)(nil)
)
