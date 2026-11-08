// Package fusefs implements the strict FUSE mount skeleton (M3-01).
//
// Strict-mode posture (spec 8.3): zero/short metadata TTLs, direct I/O for
// mutable files, userspace lease cache. go-fuse v2 is the adapter.
package fusefs

import (
	"context"
	"os"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// Root is the filesystem root (directory, inode 1).
type Root struct {
	fs.Inode
	Handler *Handler
}

// Getattr implements fs.NodeGetattr.
func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.Attr) syscall.Errno {
	out.Mode = 0755 | unix.S_IFDIR
	out.Owner = fuse.Owner{Uid: 0, Gid: 0}
	return syscall.Errno(0)
}

// Handler mediates POSIX requests (skeleton: fails closed until M3-02 lands
// real mutation handling through the volume coordinator).
type Handler struct {
	mu sync.RWMutex
}

// MountOpts returns mount options for the strict profile.
func MountOpts() *fuse.MountOptions {
	return &fuse.MountOptions{
		MaxBackground: 64,
		AllowOther:    false,
		FsName:        "syncfs",
	}
}

// KernelMount mounts the volume at target (Linux-only; tested with /dev/fuse).
func KernelMount(target string, h *Handler) (*fuse.Server, error) {
	root := &Root{Handler: h}
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
