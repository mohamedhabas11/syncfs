// Network chunk fan-out (T03): quorum durability across processes.
// Coordinator normally fans out over in-process stores; fleet deployments
// set RemotePutters so WriteChunks streams chunks to remote voters.
package volume

import (
	"context"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
)

// RemotePutter stores a chunk on a remote voter and returns its receipt.
type RemotePutter func(ctx context.Context, peerID uint64, data []byte) (chunkstore.Receipt, error)

// SetRemotePutters installs network putters keyed by voter ID.
func (c *Coordinator) SetRemotePutters(m map[uint64]RemotePutter) {
	c.remote = m
}

// SetQuorum overrides the majority computation (e.g. 2 of 3 when only one
// local store is present in this process).
func (c *Coordinator) SetQuorum(n int) {
	if n > 0 {
		c.quorum = n
	}
}

var _ = context.Background
