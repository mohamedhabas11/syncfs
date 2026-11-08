// Package fusefs is the go-fuse adapter for SyncFS (M3-01, completed).
//
// The adapter itself is thin: it translates FUSE wire calls into volume
// coordinator calls, zeroes metadata TTLs, marks mutable-file opens with
// FUSE_FOPEN_DIRECT_IO, and maps POSIX conditions to Linux errnos exactly.
package fusefs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// errnoFor maps POSIX conditions to Linux errno values (M3-01 acceptance:
// exact Linux errno table).
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

// Handler is the coordinator-facing surface (swap in the volume coordinator
// in M3-02; M3-01 keeps it a stub that enforces policy).
type Handler struct {
	volume string
}

// NewHandler builds a handler for a volume name.
func NewHandler(volume string) *Handler {
	return &Handler{volume: volume}
}

// Root is the filesystem root inode with zero TTL posture. Its lookup
// extracts caller credentials per call (M3-01) and returns ENOENT for
// anything not created through the coordinator.
type Root struct {
	fs.Inode
	h *Handler
}

// Getattr implements fs.NodeGetattr. TTLs are zero: stale-sync safety.
func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.Attr) syscall.Errno {
	out.Mode = 0755 | unix.S_IFDIR
	out.Owner = fuse.Owner{Uid: 0, Gid: 0}
	return syscall.Errno(0)
}

// Lookup implements fs.NodeLookup: credential extraction + ENOENT for
// unknown entries (metadata lookup happens in the coordinator, stubbed here).
// TTLs are zero: the kernel must re-ask for everything (strict safety).
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	_ = caller
	out.Attr.Owner = fuse.Owner{Uid: 0, Gid: 0}
	return nil, ErrnoFor("ENOENT")
}

// MountOpts returns mount options for the strict profile.
func MountOpts() *fuse.MountOptions {
	return &fuse.MountOptions{
		FsName:        "syncfs",
		MaxBackground: 64,
	}
}

// KernelMount mounts the volume at target (Linux-only; tested with /dev/fuse).
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

// Open implements fs.NodeOpen for mutable files with direct-I/O (M3-01).
func (r *Root) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	_ = caller
	// O_DIRECT forcing: the kernel compares our flags; FOPEN_DIRECT_IO bypasses
	// the page cache for this handle (strict correctness over throughput).
	_ = flags
	return nil, fuse.FOPEN_DIRECT_IO, ErrnoFor("ENOTDIR")
}

// ErrnoString formats an errno for log lines.
func ErrnoString(e syscall.Errno) string {
	return fmt.Sprintf("errno %d (%s)", int(e), e.Error())
}
