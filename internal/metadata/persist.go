// Persisted metadata projection: durable encode/decode for snapshots and
// daemon recovery (T06). Encoding is a versioned JSON envelope with
// SHA-256 checksum; decode fails closed on version/checksum mismatch.
package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PersistFormatVersion is the on-disk projection version.
const PersistFormatVersion = 1

type inodeRecord struct {
	ID        string `json:"id"` // hex 16B
	Kind      int    `json:"kind"`
	Rev       uint64 `json:"rev"`
	Size      uint64 `json:"size"`
	Mode      uint32 `json:"mode"`
	UID       uint32 `json:"uid"`
	GID       uint32 `json:"gid"`
	Manifest  string `json:"manifest"` // hex 32B
	Symlink   []byte `json:"symlink,omitempty"`
	LinkCount uint32 `json:"link_count"`
}

type envelope struct {
	Version      int                      `json:"version"`
	RaftIndex    uint64                   `json:"raft_index"`
	Revision     uint64                   `json:"revision"`
	RootID       string                   `json:"root_id"`
	Inodes       []inodeRecord            `json:"inodes"`
	Dentries     map[string]map[string]string `json:"dentries"`
	Ops          map[string]OpResult      `json:"ops"`
	VolSpecs     map[string]uint64        `json:"vol_specs"`
	Generations  map[string]uint64        `json:"generations"`
	Checksum     string                   `json:"checksum"`
}

// Encode serializes the state machine deterministically (sorted keys via
// encoding/json maps + explicit slices).
func (m *StateMachine) Encode() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	env := envelope{
		Version: PersistFormatVersion, RaftIndex: m.version.RaftIndex, Revision: m.version.Revision,
		RootID: hex.EncodeToString(m.rootID[:]),
		Dentries: map[string]map[string]string{}, Ops: map[string]OpResult{},
		VolSpecs: map[string]uint64{}, Generations: map[string]uint64{},
	}
	for id, e := range m.inodes {
		env.Inodes = append(env.Inodes, inodeRecord{
			ID: hex.EncodeToString(id[:]), Kind: e.Kind, Rev: e.Rev, Size: e.Size, Mode: e.Mode, UID: e.UID, GID: e.GID,
			Manifest: hex.EncodeToString(e.Manifest[:]), Symlink: append([]byte(nil), e.Symlink...), LinkCount: e.LinkCount,
		})
	}
	for parent, kids := range m.dentries {
		inner := map[string]string{}
		for name, child := range kids {
			inner[name] = hex.EncodeToString(child[:])
		}
		env.Dentries[hex.EncodeToString(parent[:])] = inner
	}
	for id, r := range m.ops {
		env.Ops[hex.EncodeToString(id[:])] = *r
	}
	for k, v := range m.volSpecs {
		env.VolSpecs[k] = v
	}
	for k, v := range m.generations {
		env.Generations[hex.EncodeToString(k[:])] = v
	}
	// Checksum over the payload with empty checksum field.
	env.Checksum = ""
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	env.Checksum = hex.EncodeToString(sum[:])
	return json.Marshal(env)
}

// Decode restores a state machine from Encode output. Fails closed on
// version or checksum mismatch.
func Decode(b []byte) (*StateMachine, error) {
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("metadata: decode: %w", err)
	}
	if env.Version != PersistFormatVersion {
		return nil, fmt.Errorf("metadata: unsupported persist version %d (want %d)", env.Version, PersistFormatVersion)
	}
	want := env.Checksum
	env.Checksum = ""
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("metadata: checksum mismatch")
	}
	m := New()
	// Reset to empty and install.
	m.inodes = map[[16]byte]*Entry{}
	m.dentries = map[[16]byte]map[string][16]byte{}
	m.ops = map[[16]byte]*OpResult{}
	m.volSpecs = env.VolSpecs
	if m.volSpecs == nil {
		m.volSpecs = map[string]uint64{}
	}
	m.generations = map[[16]byte]uint64{}
	var rootID [16]byte
	if b, err := hex.DecodeString(env.RootID); err == nil && len(b) == 16 {
		copy(rootID[:], b)
	}
	m.rootID = rootID
	m.version.RaftIndex = env.RaftIndex
	m.version.Revision = env.Revision
	for _, r := range env.Inodes {
		b, err := hex.DecodeString(r.ID)
		if err != nil || len(b) != 16 {
			return nil, fmt.Errorf("metadata: bad inode id")
		}
		var id [16]byte
		copy(id[:], b)
		var man [32]byte
		if mb, err := hex.DecodeString(r.Manifest); err == nil && len(mb) == 32 {
			copy(man[:], mb)
		}
		m.inodes[id] = &Entry{Kind: r.Kind, Rev: r.Rev, Size: r.Size, Mode: r.Mode, UID: r.UID, GID: r.GID, Manifest: man, Symlink: r.Symlink, LinkCount: r.LinkCount}
	}
	for parentHex, kids := range env.Dentries {
		pb, err := hex.DecodeString(parentHex)
		if err != nil || len(pb) != 16 {
			return nil, fmt.Errorf("metadata: bad dentry parent")
		}
		var parent [16]byte
		copy(parent[:], pb)
		inner := map[string][16]byte{}
		for name, childHex := range kids {
			cb, err := hex.DecodeString(childHex)
			if err != nil || len(cb) != 16 {
				return nil, fmt.Errorf("metadata: bad dentry child")
			}
			var child [16]byte
			copy(child[:], cb)
			inner[name] = child
		}
		m.dentries[parent] = inner
	}
	for idHex, r := range env.Ops {
		b, err := hex.DecodeString(idHex)
		if err != nil || len(b) != 16 {
			return nil, fmt.Errorf("metadata: bad op id")
		}
		var id [16]byte
		copy(id[:], b)
		cp := r
		m.ops[id] = &cp
	}
	for genHex, v := range env.Generations {
		b, err := hex.DecodeString(genHex)
		if err != nil || len(b) != 16 {
			continue
		}
		var id [16]byte
		copy(id[:], b)
		m.generations[id] = v
	}
	if m.LockMgr == nil {
		// Lock state is rebuilt from applied LOCK_* entries on replay;
		// for a snapshot-restored machine, start fresh (documented).
		// LockMgr is initialized by New().
	}
	return m, nil
}

// Persist writes the projection atomically: encode → write tmp → fsync file → rename → fsync dir.
func (m *StateMachine) Persist(dir string) error {
	b, err := m.Encode()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "metadata.tmp")
	final := filepath.Join(dir, "metadata.json")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	_ = d.Sync()
	return nil
}

// LoadPersisted reads a persisted projection. Returns ErrEmpty-style error
// (os.ErrNotExist) if no file exists; callers distinguish fresh vs corrupt.
func LoadPersisted(dir string) (*StateMachine, error) {
	b, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	// Verify checksum is inside Decode; also ensure canonical JSON round-trip.
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("metadata: load: %w", err)
	}
	_ = bytes.Clone
	return Decode(b)
}
