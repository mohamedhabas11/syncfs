// Package wal implements the crash-safe Raft WAL + snapshot store (M2-01).
//
// Persistence contract (spec 8.4): WAL entries and hard state are fdatasynced
// BEFORE the associated Raft messages are transmitted. Snapshots are
// persisted before being reported applied. The Ready driver's ordering hooks
// (M2-04) call SaveReady/PostReady; tests instrument the hook points below.
//
// Record format (file <dir>/wal.log, append-only):
//
//	[crc32 IEEE (4B)][len (4B BE)][payload]
//
// payload is a raftpb.Entry or a raftpb.HardState marshaled via protobuf,
// tagged by a leading byte 0x01 (entry) / 0x02 (hard state). A torn trailing
// record (CRC/length mismatch) is truncated on open; entries after the
// truncation point never happened (they were never acked).
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/mohamedhabas11/syncfs/internal/raftstore/snapshot"
	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

const (
	tagEntry     = 0x01
	tagHardState = 0x02
	maxRecord    = 1 << 30
)

// Errors returned by the store.
var (
	ErrTornRecord = errors.New("wal: torn or corrupt trailing record truncated")
	ErrIndexGap   = errors.New("wal: entry index gap")
	ErrEmpty      = errors.New("wal: not initialized")
)

// Hooks fire deterministically for crash tests (testkit/crash installs them).
type Hooks struct {
	// AfterRecordPersist runs after each record is written (before sync).
	AfterRecordPersist func(ctx HookContext)
	// AfterSync runs after fdatasync of the WAL file.
	AfterSync func(ctx HookContext)
}

type HookContext struct {
	Kind      string // "entry" | "hardstate" | "snapshot"
	Index     uint64
	Term      uint64
	AppendSeq uint64
}

// Options for Open.
type Options struct {
	Hooks *Hooks
	// Dir is the WAL directory (created if missing).
	Dir string
	// InitialPeers bootstraps the ConfState on a fresh WAL (first node).
	InitialPeers []uint64
}

// Storage implements raft.Storage (read side) with durable write side.
type Storage struct {
	mu    sync.Mutex
	dir   string
	file  *os.File
	hooks *Hooks

	hardState raftpb.HardState
	snapshot  raftpb.Snapshot
	// ents[i] has index i + snapshot.Metadata.Index + 1
	ents []raftpb.Entry

	initialPeers []uint64

	seq          uint64
	closed       bool
	truncatedErr error
}

// Open replays (or creates) the WAL at dir and returns a Storage.
func Open(opts Options) (*Storage, error) {
	f, err := os.OpenFile(filepath.Join(opts.Dir, "wal.log"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	s := &Storage{dir: opts.Dir, file: f, hooks: opts.Hooks, initialPeers: opts.InitialPeers}
	if err := s.replay(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

func (s *Storage) replay() error {
	data, err := io.ReadAll(s.file)
	if err != nil {
		return fmt.Errorf("wal: read: %w", err)
	}
	off := 0
	for off < len(data) {
		rec, n, err := decodeRecord(data[off:])
		if err != nil {
			s.truncatedErr = fmt.Errorf("%w: %v", ErrTornRecord, err)
			break
		}
		if err := s.applyRecord(rec); err != nil {
			return err
		}
		off += n
	}
	// truncate file at the torn point
	if s.truncatedErr != nil {
		if err := s.file.Truncate(int64(off)); err != nil {
			return err
		}
		if err := s.file.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) applyRecord(rec record) error {
	switch rec.tag {
	case tagEntry:
		var e raftpb.Entry
		if err := e.Unmarshal(rec.payload); err != nil {
			return fmt.Errorf("%w: entry unmarshal: %v", ErrTornRecord, err)
		}
		return s.appendEntryLocked(e)
	case tagHardState:
		var hs raftpb.HardState
		if err := hs.Unmarshal(rec.payload); err != nil {
			return fmt.Errorf("%w: hardstate unmarshal: %v", ErrTornRecord, err)
		}
		s.hardState = hs
		return nil
	}
	return fmt.Errorf("wal: unknown record tag %d", rec.tag)
}

func (s *Storage) appendEntryLocked(e raftpb.Entry) error {
	// Entry zero is the snapshot dummy marker; skip index accounting for it.
	if e.Index == 0 {
		return nil
	}
	s.ents = append(s.ents, e)
	last := s.snapshot.Metadata.Index + uint64(len(s.ents))
	if e.Index != last {
		return fmt.Errorf("%w (got %d want %d)", ErrIndexGap, e.Index, last)
	}
	return nil
}

// writeRecord appends one record and fdatasyncs before returning.
func (s *Storage) writeRecord(tag byte, payload []byte) error {
	crc := crc32.ChecksumIEEE(payload)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], crc)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(payload)))
	tmp := make([]byte, 0, 8+1+len(payload))
	tmp = append(tmp, hdr[:]...)
	tmp = append(tmp, tag)
	tmp = append(tmp, payload...)
	if _, err := s.file.Write(tmp); err != nil {
		return err
	}
	if s.hooks != nil && s.hooks.AfterRecordPersist != nil {
		s.hooks.AfterRecordPersist(HookContext{Kind: kindName(tag), AppendSeq: s.seq})
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("wal: fdatasync: %w", err)
	}
	if s.hooks != nil && s.hooks.AfterSync != nil {
		s.hooks.AfterSync(HookContext{Kind: kindName(tag), AppendSeq: s.seq})
	}
	s.seq++
	return nil
}

func kindName(tag byte) string {
	if tag == tagHardState {
		return "hardstate"
	}
	return "entry"
}

// SaveReady persists a raft Ready (entries + hard state + snapshot) with the
// ordering of spec 8.4. It MUST be called before Ready.Messages are sent
// (enforced by the M2-04 driver, not here).
func (s *Storage) SaveReady(r raft.Ready) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("wal: closed")
	}
	for _, e := range r.Entries {
		b, err := e.Marshal()
		if err != nil {
			return err
		}
		if err := s.writeRecord(tagEntry, b); err != nil {
			return err
		}
		if err := s.appendEntryLocked(e); err != nil {
			return err
		}
	}
	if !eQual(r.HardState, s.hardState) {
		b, err := r.HardState.Marshal()
		if err != nil {
			return err
		}
		if err := s.writeRecord(tagHardState, b); err != nil {
			return err
		}
		s.hardState = r.HardState
	}
	if r.Snapshot.Metadata.Index != 0 {
		if err := snapshot.WriteSnapshotFile(s.dir, &r.Snapshot); err != nil {
			return err
		}
		if err := s.applySnapshotLocked(r.Snapshot); err != nil {
			return err
		}
	}
	return nil
}

func eQual(a, b raftpb.HardState) bool {
	return a.Term == b.Term && a.Vote == b.Vote && a.Commit == b.Commit
}

// applySnapshotLocked discards entries covered by the snapshot.
func (s *Storage) applySnapshotLocked(snap raftpb.Snapshot) error {
	idx := snap.Metadata.Index
	if idx == 0 {
		return nil
	}
	s.snapshot = snap
	keep := s.ents
	s.ents = nil
	for _, e := range keep {
		if e.Index > idx {
			s.ents = append(s.ents, e)
		}
	}
	s.hardState.Commit = maxUint64(s.hardState.Commit, idx)
	return nil
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// --- raft.Storage read side ---

func (s *Storage) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cc := s.snapshot.Metadata.ConfState
	if len(cc.Voters) == 0 && len(s.initialPeers) > 0 {
		// bootstrap: fresh WAL gets the initial membership preloaded
		cc.Voters = append([]uint64{}, s.initialPeers...)
		return s.hardState, cc, nil
	}
	return s.hardState, cc, nil
}

func (s *Storage) Entries(lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.snapshot.Metadata.Index
	first := base + 1
	if lo < first {
		return nil, raft.ErrCompacted
	}
	last := base + uint64(len(s.ents))
	if hi > last+1 {
		hi = last + 1
	}
	var out []raftpb.Entry
	var total uint64
	for i := lo; i < hi; i++ {
		e := s.ents[i-base-1]
		size := uint64(e.Size())
		if total+size > maxSize && len(out) > 0 {
			break
		}
		total += size
		out = append(out, e)
	}
	return out, nil
}

func (s *Storage) Term(i uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.snapshot.Metadata.Index
	if i < base {
		return 0, raft.ErrCompacted
	}
	if i == base {
		return s.snapshot.Metadata.Term, nil
	}
	idx := i - base - 1
	if idx >= uint64(len(s.ents)) {
		return 0, errors.New("wal: entry not found")
	}
	return s.ents[idx].Term, nil
}

func (s *Storage) LastIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.Metadata.Index + uint64(len(s.ents)), nil
}

func (s *Storage) FirstIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.Metadata.Index + 1, nil
}

func (s *Storage) Snapshot() (raftpb.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Metadata.Index == 0 {
		return raftpb.Snapshot{}, raft.ErrSnapshotTemporarilyUnavailable
	}
	return s.snapshot, nil
}

// Compact drops entries with index <= compactIndex (post-snapshot GC).
func (s *Storage) Compact(compactIndex uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.snapshot.Metadata.Index
	if compactIndex <= base {
		return nil
	}
	var keep []raftpb.Entry
	for _, e := range s.ents {
		if e.Index > compactIndex {
			keep = append(keep, e)
		}
	}
	s.ents = keep
	return nil
}

// Close fdatasyncs and closes the WAL.
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.file.Sync(); err != nil {
		return err
	}
	return s.file.Close()
}

func (s *Storage) TruncatedErr() error { return s.truncatedErr }

// --- record encoding ---

type record struct {
	tag     byte
	payload []byte
}

func decodeRecord(b []byte) (record, int, error) {
	if len(b) < 8 {
		return record{}, 0, io.ErrUnexpectedEOF
	}
	crc := binary.LittleEndian.Uint32(b[0:])
	n := binary.LittleEndian.Uint32(b[4:])
	if n == 0 || n > maxRecord || uint64(8)+uint64(1)+uint64(n) > uint64(len(b)) {
		return record{}, 0, io.ErrUnexpectedEOF
	}
	payload := b[8+1 : 8+1+n]
	if crc32.ChecksumIEEE(payload) != crc {
		return record{}, 0, errors.New("crc mismatch")
	}
	return record{tag: b[8], payload: bytes.Clone(payload)}, 8 + 1 + int(n), nil
}
