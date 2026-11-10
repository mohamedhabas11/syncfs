// HTTP wire client for VM test fleet: implements fusefs.Syncer over the
// daemon HTTP endpoints (/volume/chunks + /volume/command). The mount
// stays strict: any HTTP failure surfaces as EIO, never silent success.
package client

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"
)

// WireSyncer routes FUSE durability through a daemon over HTTP.
type WireSyncer struct {
	Base   string // e.g. http://127.0.0.1:18081
	Client *http.Client
	// Inode mapping: Handler name -> metadata InodeID (16B) + file offset
	// state is owned by the Handler memory; this only proves durability.
	names map[string][16]byte
}

func NewWireSyncer(base string) *WireSyncer {
	return &WireSyncer{Base: base, Client: &http.Client{Timeout: 10 * time.Second}, names: map[string][16]byte{}}
}

func (w *WireSyncer) httpBase() string {
	if w.Client == nil {
		w.Client = &http.Client{Timeout: 10 * time.Second}
	}
	return w.Base
}

func (w *WireSyncer) post(path string, body []byte, ct string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, w.httpBase()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("daemon %s: %d", path, resp.StatusCode)
	}
	return rb, nil
}

// chunkReceipt is one durable chunk receipt from the daemon's /volume/chunks.
type chunkReceipt struct {
	ChunkID string `json:"chunk_id"`
	PeerID  uint64 `json:"peer_id"`
}

// chunksResponse is the /volume/chunks JSON envelope.
type chunksResponse struct {
	Receipts []chunkReceipt `json:"receipts"`
}

// postChunks proves chunk quorum with the daemon and returns durable
// receipts (Iter-8-07: FUSE → daemon write path attaches these to WRITE_RANGES).
func (w *WireSyncer) postChunks(data []byte) ([]chunkReceipt, error) {
	rb, err := w.post("/volume/chunks", data, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	var cr chunksResponse
	if err := json.Unmarshal(rb, &cr); err != nil {
		return nil, fmt.Errorf("daemon /volume/chunks: decode receipts: %w", err)
	}
	return cr.Receipts, nil
}

// preparedReceipts converts durable chunk receipts into protobuf
// PreparedChunkReceipts (payload before reference: commit needs majority).
func preparedReceipts(receipts []chunkReceipt) []*syncfsapi.PreparedChunkReceipt {
	var out []*syncfsapi.PreparedChunkReceipt
	seen := map[string]bool{}
	for _, r := range receipts {
		if len(r.ChunkID) != 64 {
			continue
		}
		var cid [32]byte
		if _, err := hex.Decode(cid[:], []byte(r.ChunkID)); err != nil {
			continue
		}
		pid := []byte{byte(r.PeerID)}
		key := string(cid[:]) + string(pid)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &syncfsapi.PreparedChunkReceipt{ChunkId: cid[:], PeerId: pid})
	}
	return out
}

func (w *WireSyncer) ensureFile(name string) ([16]byte, error) {
	if id, ok := w.names[name]; ok {
		return id, nil
	}
	// Deterministic op ID from (parent inode, name) — the daemon derives the
	// inode ID as a pure function of (opID, parent, name) (spec: replay is a
	// pure function), so retries of the same file map to the same inode.
	parent := [16]byte{} // root-level file (root inode id = 0)
	h := sha256.New()
	h.Write(parent[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var opID [16]byte
	copy(opID[:], sum[:16])
	cmd := &syncfsapi.Command{
		OperationId: opID[:],
		Kind:        syncfsapi.CommandKind_CREATE,
		Payload: &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: parent[:],
			Name:          name,
		}},
	}
	body, err := proto.Marshal(cmd)
	if err != nil {
		return [16]byte{}, err
	}
	if _, err := w.post("/volume/command", body, "application/protobuf"); err != nil {
		return [16]byte{}, err
	}
	// Inode ID = sha256(opID||parent||name)[:16] — identical derivation to the
	// daemon's StateMachine.doCreate, given the deterministic opID above.
	ih := sha256.New()
	ih.Write(opID[:])
	ih.Write(parent[:])
	ih.Write([]byte(name))
	isum := ih.Sum(nil)
	var id [16]byte
	copy(id[:], isum[:16])
	w.names[name] = id
	return id, nil
}

func (w *WireSyncer) OnWrite(name string, at int64, data []byte) error {
	// Durable chunk prepare: quorum-prove the bytes, keep the receipts.
	receipts, err := w.postChunks(data)
	if err != nil {
		return err
	}
	id, err := w.ensureFile(name)
	if err != nil {
		return err
	}
	manifest := blake3.Sum256(data)
	var opID [16]byte
	if _, err := rand.Read(opID[:]); err != nil {
		return err
	}
	var cmd syncfsapi.Command
	cmd.OperationId = opID[:]
	cmd.Kind = syncfsapi.CommandKind_WRITE_RANGES
	cmd.Payload = &syncfsapi.Command_WriteRanges{WriteRanges: &syncfsapi.WriteRangesPayload{
		InodeId:     id[:],
		NewManifest: manifest[:],
	}}
	cmd.PreparedChunks = preparedReceipts(receipts)
	body, err := proto.Marshal(&cmd)
	if err != nil {
		return fmt.Errorf("client: marshal WRITE_RANGES: %w", err)
	}
	// Metadata commit (idempotent via op-ID); failure surfaces, never silent.
	if _, err := w.post("/volume/command", body, "application/protobuf"); err != nil {
		return err
	}
	return nil
}

func (w *WireSyncer) OnTruncate(name string, size uint64) error {
	id, err := w.ensureFile(name)
	if err != nil {
		return err
	}
	var opID [16]byte
	if _, err := rand.Read(opID[:]); err != nil {
		return err
	}
	cmd := &syncfsapi.Command{
		OperationId: opID[:],
		Kind:        syncfsapi.CommandKind_TRUNCATE,
		Payload:     &syncfsapi.Command_Truncate{Truncate: &syncfsapi.TruncatePayload{InodeId: id[:], Size: size}},
	}
	body, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("client: marshal TRUNCATE: %w", err)
	}
	if _, err := w.post("/volume/command", body, "application/protobuf"); err != nil {
		return err
	}
	return nil
}

func (w *WireSyncer) OnFsync(name string) error {
	// The write path already proved chunk quorum + metadata commit (Raft
	// commit barrier); fsync is the client's durable barrier to the daemon.
	// Fail closed if the file entry is not resolvable.
	if _, err := w.ensureFile(name); err != nil {
		return err
	}
	return nil
}

func (w *WireSyncer) OnFsyncDir(name string) error { return nil }
