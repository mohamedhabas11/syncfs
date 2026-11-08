// Package chunkstore stores immutable, checksummed 64 KiB content chunks
// in append-only pack files with a Pebble-backed index (M2-02).
//
// Durability contract: Put fsyncs the pack file and the packs directory
// before indexing, so a returned receipt is durable. Pebble writes use
// WriteOptions.Sync=true. A torn trailing pack record is dropped on reopen.
package chunkstore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/cockroachdb/pebble"
	"lukechampine.com/blake3"
)

// ChunkSize is the fixed copy-on-write chunk size (spec 7.2).
const ChunkSize = 65536

// Errors.
var (
	ErrCorrupt  = errors.New("chunkstore: data corrupt")
	ErrNotFound = errors.New("chunkstore: chunk not found")
	ErrBadSize  = errors.New("chunkstore: bad chunk size")
	ErrClosed   = errors.New("chunkstore: store closed")
)

// ChunkID is the BLAKE3-256 content address.
type ChunkID [32]byte

// Receipt is issued when a chunk is durable locally (majority receipts are
// the write coordinator's job, M2-05).
type Receipt struct {
	ChunkID         ChunkID
	StoreGeneration uint64
	VerifiedBytes   uint64
}

// location is the Pebble-index value: pack file number + record offset.
type location struct {
	packNumber uint32
	offset     uint64
}

type Store struct {
	mu         sync.Mutex
	dir        string
	db         *pebble.DB
	packs      []*os.File
	generation uint64
	closed     bool
	writeSeq   uint64
}

// Open creates (or reopens) the store at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db, err := pebble.Open(filepath.Join(dir, "pebble"), nil)
	if err != nil {
		return nil, fmt.Errorf("chunkstore: pebble: %w", err)
	}
	s := &Store{dir: dir, db: db}
	// Reopen all existing packs and rebuild/verify durable state.
	packs, err := listPacks(dir)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, n := range packs {
		if err := s.openPack(n); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return s, nil
}

func listPacks(dir string) ([]uint32, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []uint32
	for _, e := range ents {
		if len(e.Name()) > 9 && e.Name()[:5] == "pack-" && e.Name()[5:13] != "" {
			var n uint32
			if _, err := fmt.Sscanf(e.Name(), "pack-%08d.pack", &n); err == nil {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func (s *Store) openPack(n uint32) error {
	for uint32(len(s.packs)) <= n {
		s.packs = append(s.packs, nil)
	}
	f, err := os.OpenFile(packPath(s.dir, n), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	s.packs[n] = f
	// Scan the pack to verify already-indexed records + drop torn tail.
	if err := s.scanPack(f); err != nil {
		return err
	}
	if uint64(n) >= s.generation {
		s.generation = uint64(n) + 1
	}
	return nil
}

func packPath(dir string, n uint32) string {
	return filepath.Join(dir, fmt.Sprintf("pack-%08d.pack", n))
}

func (s *Store) scanPack(f *os.File) error {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	off := 0
	for off < len(data) {
		id, offset, length, err := readRecord(data, off)
		if err != nil {
			// torn tail: truncate at last good record and stop
			_ = f.Truncate(int64(off))
			_ = f.Sync()
			return nil
		}
		_ = id
		_ = offset
		_ = length
		off = offset + length
	}
	return nil
}

func currentPackFile(s *Store) *os.File {
	// last pack (packs are appended in order)
	return s.packs[len(s.packs)-1]
}

// Put stores data<=ChunkSize idempotently; returns a durable receipt.
func (s *Store) Put(data []byte) (ChunkID, Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ChunkID{}, Receipt{}, ErrClosed
	}
	if len(data) == 0 || len(data) > ChunkSize {
		return ChunkID{}, Receipt{}, ErrBadSize
	}
	sum := blake3.Sum256(data)
	var id ChunkID
	copy(id[:], sum[:])

	if loc, err := s.lookupLocked(id); err == nil {
		_ = loc
		// already durable: idempotent success, no rewrite
		return id, s.receiptLocked(id, uint64(len(data))), nil
	}

	if len(s.packs) == 0 {
		if err := s.openPack(0); err != nil {
			return ChunkID{}, Receipt{}, err
		}
	}
	f := currentPackFile(s)
	rec := encodeRecord(id, data)
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return ChunkID{}, Receipt{}, err
	}
	if _, err := f.Write(rec); err != nil {
		return ChunkID{}, Receipt{}, err
	}
	if err := f.Sync(); err != nil {
		return ChunkID{}, Receipt{}, fmt.Errorf("chunkstore: pack fsync: %w", err)
	}
	if err := syncDir(f.Name()); err != nil {
		return ChunkID{}, Receipt{}, err
	}
	// durability before indexing: index write with Sync
	if err := s.indexLocked(id, location{packNumber: packNumberOf(s, f), offset: uint64(off)}); err != nil {
		return ChunkID{}, Receipt{}, err
	}
	s.writeSeq++
	return id, s.receiptLocked(id, uint64(len(data))), nil
}

func packNumberOf(s *Store, f *os.File) uint32 {
	for i, p := range s.packs {
		if p == f {
			return uint32(i)
		}
	}
	return 0
}

func (s *Store) receiptLocked(id ChunkID, bytes uint64) Receipt {
	return Receipt{ChunkID: id, StoreGeneration: s.generation, VerifiedBytes: bytes}
}

// Get returns chunk bytes after BLAKE3 verification.
func (s *Store) Get(id ChunkID) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	loc, err := s.lookupLocked(id)
	if err != nil {
		return nil, err
	}
	if int(loc.packNumber) >= len(s.packs) {
		return nil, ErrNotFound
	}
	data, err := readAt(s.packs[loc.packNumber], loc.offset)
	if err != nil {
		return nil, err
	}
	if blake3.Sum256(data) != id {
		return nil, ErrCorrupt
	}
	return data, nil
}

// Has reports presence in the index.
func (s *Store) Has(id ChunkID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.lookupLocked(id)
	return err == nil
}

// Close fsyncs and closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	for _, f := range s.packs {
		if f == nil {
			continue
		}
		if err := f.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = f.Close()
	}
	if err := s.db.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Store) lookupLocked(id ChunkID) (location, error) {
	v, closer, err := s.db.Get(id[:])
	if err != nil {
		return location{}, ErrNotFound
	}
	defer closer.Close()
	var l location
	l.packNumber = binary.BigEndian.Uint32(v[:4])
	l.offset = binary.BigEndian.Uint64(v[4:])
	return l, nil
}

func (s *Store) indexLocked(id ChunkID, l location) error {
	var v [12]byte
	binary.BigEndian.PutUint32(v[0:], l.packNumber)
	binary.BigEndian.PutUint64(v[4:], l.offset)
	return s.db.Set(id[:], v[:], pebble.Sync)
}

// encodeRecord: [crc32(4)][len(3)][hash(32)][data]
func encodeRecord(id ChunkID, data []byte) []byte {
	rec := make([]byte, 0, 4+3+32+len(data))
	crc := crc32.ChecksumIEEE(data)
	var h [7]byte
	binary.BigEndian.PutUint32(h[0:], crc)
	h[4], h[5], h[6] = byte(len(data)>>16), byte(len(data)>>8), byte(len(data))
	rec = append(rec, h[:]...)
	rec = append(rec, id[:]...)
	rec = append(rec, data...)
	return rec
}

// readRecord parses a record at off; returns id, offset (of data start), length (data len).
func readRecord(b []byte, off int) (ChunkID, int, int, error) {
	var id ChunkID
	if off+4+3+32 > len(b) {
		return id, 0, 0, io.ErrUnexpectedEOF
	}
	wantCRC := binary.BigEndian.Uint32(b[off:])
	n := int(b[off+4])<<16 | int(b[off+5])<<8 | int(b[off+6])
	off2 := off + 4 + 3 + 32
	if off2+n > len(b) {
		return id, 0, 0, io.ErrUnexpectedEOF
	}
	data := b[off2 : off2+n]
	if crc32.ChecksumIEEE(data) != wantCRC {
		return id, 0, 0, errors.New("crc")
	}
	copy(id[:], b[off+4+3:off+4+3+32])
	return id, off2, n, nil
}

func readAt(f *os.File, off uint64) ([]byte, error) {
	if _, err := f.Seek(int64(off), io.SeekStart); err != nil {
		return nil, err
	}
	var hdr [4 + 3 + 32]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, ErrCorrupt
	}
	n := int(hdr[4])<<16 | int(hdr[5])<<8 | int(hdr[6])
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, ErrCorrupt
	}
	return buf, nil
}

func syncDir(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// digestBytes is unused but keeps the sha256 reference to document the media;
// BLAKE3 is the only integrity authority (quic/TLS is transport integrity).
var _ = sha256.New

// --- test hooks ---

// CorruptForTest flips one byte inside the pack region of id.
func (s *Store) CorruptForTest(id ChunkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, err := s.lookupLocked(id)
	if err != nil {
		return err
	}
	f := s.packs[loc.packNumber]
	b := []byte{0xff}
	if _, err := f.WriteAt(b, int64(loc.offset+4+3+32+10)); err != nil {
		return err
	}
	return f.Sync()
}

// SimulateTornTailForTest truncates the last pack mid-record (torn write).
func (s *Store) SimulateTornTailForTest() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := currentPackFile(s)
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if off < 5 {
		return errors.New("too small")
	}
	// Cut the last 3 bytes off (the fsync-mark tail of last record): the record
	// becomes truncated.
	if err := f.Truncate(off - 3); err != nil {
		return err
	}
	return f.Sync()
}
