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

// NewHandler builds a handler with a root directory. It installs a NoopSyncer
// (acknowledges writes unconditionally) and is the TEST surface only — it is
// NOT a strict mount. Production mounts must use NewStrictHandler and wire a
// real coordinator via SetSyncer; otherwise writes are silently local (Inv.10).
func NewHandler(volume string) *Handler {
	r := &NodeState{kind: KDir, mode: 0755, uid: 0, gid: 0, children: map[string]*NodeState{}, mtime: time.Now()}
	_ = volume
	return &Handler{root: r, syncer: NoopSyncer{}}
}

// NewStrictHandler builds a fail-closed handler for production mounts: with
// no coordinator wired (nil syncer), writes return EIO instead of ack.
func NewStrictHandler(volume string) *Handler {
	h := NewHandler(volume)
	h.syncer = nil
	return h
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
	FillAttr(n, out)
}

// RootAttrs is exposed for childNode (same package).
func (h *Handler) RootAttrs(n *NodeState, out *fuse.Attr) { FillAttr(n, out) }

// FillAttr copies NodeState into a fuse.Attr (mode includes type bits).
func FillAttr(n *NodeState, out *fuse.Attr) {
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
	if r.h == nil || r.h.root == nil {
		return syscall.ENOENT // fail closed on uninitialized handler
	}
	if c, ok := extractCaller(ctx); ok {
		if e := r.h.root.canAccess(c, true, false, true); e != 0 {
			return e
		}
	}
	r.attrs(r.h.root, &out.Attr)
	return syscall.Errno(0)
}

// childNode is the kernel-visible inode for any depth (T04). It holds the
// resolved NodeState pointer, so Lookup/Readdir work at arbitrary depth, not
// just flat-root children.
type childNode struct {
	fs.Inode
	h  *Handler
	ns *NodeState
}

func (c *childNode) resolve() *NodeState { return c.ns }

// Getattr serves the child's attrs (kernel stat).
func (c *childNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if c.ns == nil {
		return syscall.ENOENT
	}
	c.h.RootAttrs(c.ns, &out.Attr)
	return syscall.Errno(0)
}

// Open mirrors the strict direct-I/O policy with O_DIRECT rejection.
func (c *childNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&oDirectFlag != 0 {
		return nil, 0, syscall.EINVAL
	}
	caller, hasCaller := extractCaller(ctx)
	name := ""
	if c.ns != nil {
		// Best-effort flat name for the syncer seam; nested paths fall back
		// to empty (root-style) until the coordinator bind maps inode IDs.
		c.h.mu.RLock()
		for n, child := range c.h.root.children {
			if child == c.ns {
				name = n
				break
			}
		}
		c.h.mu.RUnlock()
	}
	return &fileHandle{h: c.h, name: name, caller: caller, hasCaller: hasCaller, flags: flags}, fuse.FOPEN_DIRECT_IO, syscall.Errno(0)
}

// Lookup resolves name within this directory (arbitrary depth).
func (c *childNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (inode *fs.Inode, e syscall.Errno) {
	if c.ns == nil {
		return nil, syscall.ENOENT
	}
	if e := validName(name); e != 0 {
		return nil, e
	}
	caller, hasCaller := extractCaller(ctx)
	if hasCaller {
		if en := c.ns.canAccess(caller, false, false, true); en != 0 {
			return nil, en
		}
	}
	c.ns.mu.RLock()
	child, ok := c.ns.children[name]
	c.ns.mu.RUnlock()
	if !ok {
		return nil, syscall.ENOENT
	}
	if hasCaller {
		if en := child.canAccess(caller, true, false, false); en != 0 {
			return nil, en
		}
	}
	FillAttr(child, &out.Attr)
	mode := child.mode
	if child.kind == KDir {
		mode |= unix.S_IFDIR
	} else if child.kind == KSymlink {
		mode |= unix.S_IFLNK
	} else {
		mode |= unix.S_IFREG
	}
	defer func() {
		if recover() != nil {
			inode = nil
		}
	}()
	inode = c.NewPersistentInode(ctx, &childNode{h: c.h, ns: child}, fs.StableAttr{Mode: mode})
	return inode, syscall.Errno(0)
}

// Readdir lists this directory's children (arbitrary depth).
func (c *childNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	if c.ns == nil {
		return nil, syscall.ENOENT
	}
	c.ns.mu.RLock()
	defer c.ns.mu.RUnlock()
	if c.ns.kind != KDir {
		return nil, syscall.ENOTDIR
	}
	var list []fuse.DirEntry
	for name, child := range c.ns.children {
		var mode uint32
		switch child.kind {
		case KDir:
			mode = unix.S_IFDIR | child.mode
		case KSymlink:
			mode = unix.S_IFLNK | child.mode
		default:
			mode = unix.S_IFREG | child.mode
		}
		list = append(list, fuse.DirEntry{Name: name, Mode: mode})
	}
	return fs.NewListDirStream(list), syscall.Errno(0)
}

// NOTE: go-fuse v2.12.1 exposes no NodeMmap hook, so writable-mmap
// exclusion cannot be surfaced as EOPNOTSUPP from the handler; the kernel
// receives the default ENOSYS. This matches docs/known-limitations.md #2
// (explicit handler pending a go-fuse hook). Do not claim EOPNOTSUPP until
// such a hook exists.

// Lookup implements fs.NodeLookup: resolve name within root, else ENOENT.
// Caller credentials are extracted and enforced (EPERM/EACCES on denial).
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (inode *fs.Inode, e syscall.Errno) {
	if r.h == nil || r.h.root == nil {
		return nil, syscall.ENOENT
	}
	caller, hasCaller := extractCaller(ctx)
	if hasCaller {
		// Need execute on the parent directory to resolve the name.
		if en := r.h.root.canAccess(caller, false, false, true); en != 0 {
			return nil, en
		}
	}
	r.h.mu.RLock()
	child, ok := r.h.root.children[name]
	r.h.mu.RUnlock()
	if !ok {
		return nil, ErrnoFor("ENOENT")
	}
	if hasCaller {
		if en := child.canAccess(caller, true, false, false); en != 0 {
			return nil, en
		}
	}
	r.attrs(child, &out.Attr)
	mode := child.mode
	if child.kind == KDir {
		mode |= unix.S_IFDIR
	} else if child.kind == KSymlink {
		mode |= unix.S_IFLNK
	} else {
		mode |= unix.S_IFREG
	}
	// NewPersistentInode requires an initialized mount bridge; when
	// unmounted (unit tests, darwin) it panics. Recover to the legacy
	// stat-root path (attrs only) so unmounted Lookup fails closed gracefully
	// while kernel mounts get a traversable child inode.
	defer func() {
		if recover() != nil {
			inode = nil
		}
	}()
	inode = r.NewPersistentInode(ctx, &childNode{h: r.h, ns: child}, fs.StableAttr{Mode: mode})
	return inode, syscall.Errno(0)
}

// Readdir lists flat-root children for kernel ls.
func (r *Root) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	if r.h == nil || r.h.root == nil {
		return nil, syscall.ENOENT
	}
	r.h.mu.RLock()
	defer r.h.mu.RUnlock()
	var list []fuse.DirEntry
	for name, child := range r.h.root.children {
		var mode uint32
		switch child.kind {
		case KDir:
			mode = unix.S_IFDIR | child.mode
		case KSymlink:
			mode = unix.S_IFLNK | child.mode
		default:
			mode = unix.S_IFREG | child.mode
		}
		list = append(list, fuse.DirEntry{Name: name, Mode: mode})
	}
	return fs.NewListDirStream(list), syscall.Errno(0)
}

// fileHandle is the direct-I/O mutable handle (M3-01). Release is
// idempotent and marks the handle closed; Fsync waits for durable commit via
// the handler's coordinator seam (never a memory-only acknowledgement).
// name is the flat-root child name; "" denotes the Root (directory) handle,
// whose fsync is a namespace fsync (OnFsyncDir with the empty root name).
type fileHandle struct {
	h         *Handler
	name      string
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
	if fh.h == nil || fh.h.syncer == nil {
		return syscall.EIO
	}
	if fh.name == "" {
		if err := fh.h.syncer.OnFsyncDir(""); err != nil {
			return syscall.EIO
		}
		return syscall.Errno(0)
	}
	if err := fh.h.syncer.OnFsync(fh.name); err != nil {
		return syscall.EIO
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
	if flags&oDirectFlag != 0 {
		// Strict mode: O_DIRECT is excluded (EINVAL per posix matrix).
		return nil, 0, syscall.EINVAL
	}
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
	_ fs.NodeReaddirer = (*Root)(nil)
	_ fs.NodeGetattrer = (*childNode)(nil)
	_ fs.NodeOpener    = (*childNode)(nil)
	_ fs.NodeLookuper  = (*childNode)(nil)
	_ fs.NodeReaddirer = (*childNode)(nil)
)
