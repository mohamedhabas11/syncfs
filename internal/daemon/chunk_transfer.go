package daemon

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/transport"
)

// putChunkOnPeer performs a quorum Put over QUIC ClassChunk.
// Wire format per stream (bounded, binary — not protobuf):
//
//	request:  [4B BE len][chunk bytes]  (len ≤ 65536)
//	response: [32B ChunkID][8B BE VerifiedBytes]
//
// The receiver verifies BLAKE3 inside chunkstore.Put; the sender verifies
// the returned ID matches the expected digest.
func (d *Daemon) putChunkOnPeer(ctx context.Context, peerID uint64, data []byte) (chunkstore.Receipt, error) {
	conn, err := d.peerConn(ctx, peerID)
	if err != nil {
		return chunkstore.Receipt{}, err
	}
	st, err := transport.OpenClassifiedStream(conn, d.self, transport.ClassChunk)
	if err != nil {
		d.peerMu.Lock()
		delete(d.peerConns, peerID)
		d.peerMu.Unlock()
		return chunkstore.Receipt{}, err
	}
	defer st.Close()
	if _, err := st.Write([]byte{3}); err != nil { // 3 = ClassChunk
		return chunkstore.Receipt{}, err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := st.Write(hdr[:]); err != nil {
		return chunkstore.Receipt{}, err
	}
	if _, err := st.Write(data); err != nil {
		return chunkstore.Receipt{}, err
	}
	// Read response (receiver writes after Put).
	var idBytes [32]byte
	if _, err := io.ReadFull(st, idBytes[:]); err != nil {
		return chunkstore.Receipt{}, err
	}
	var verified [8]byte
	if _, err := io.ReadFull(st, verified[:]); err != nil {
		return chunkstore.Receipt{}, err
	}
	var id chunkstore.ChunkID
	copy(id[:], idBytes[:])
	return chunkstore.Receipt{ChunkID: id, PeerID: peerID, VerifiedBytes: binary.BigEndian.Uint64(verified[:])}, nil
}

// acceptChunk handles one inbound ClassChunk stream (server side).
func (d *Daemon) acceptChunk(st interface{ Read([]byte) (int, error) }) error {
	var hdr [4]byte
	if _, err := io.ReadFull(st, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > chunkstore.ChunkSize {
		return fmt.Errorf("chunk len %d out of range", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(st, data); err != nil {
		return err
	}
	id, rc, err := d.chunk.Put(data)
	if err != nil {
		return err
	}
	// Response: need a writer; caller passes a quic.Stream.
	if w, ok := st.(interface{ Write([]byte) (int, error) }); ok {
		if _, err := w.Write(id[:]); err != nil {
			return err
		}
		var vb [8]byte
		binary.BigEndian.PutUint64(vb[:], rc.VerifiedBytes)
		if _, err := w.Write(vb[:]); err != nil {
			return err
		}
	}
	return nil
}

var _ = transport.ClassChunk
