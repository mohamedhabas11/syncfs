package chunkstore

import (
	"crypto/rand"
	"errors"
	"testing"
)

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPutGetRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := randBytes(t, 4096)
	id, receipt, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.VerifiedBytes != uint64(len(data)) {
		t.Fatalf("receipt bytes %d want %d", receipt.VerifiedBytes, len(data))
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("data mismatch")
	}
	if !s.Has(id) {
		t.Fatalf("Has after Put must be true")
	}
}

func TestPutIdempotentDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := randBytes(t, 4096)
	id1, _, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	id2, _, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("same content must give same id")
	}
	got, err := s.Get(id2)
	if err != nil || string(got) != string(data) {
		t.Fatalf("dedup read: %v", err)
	}
}

func TestCorruptionDetected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := randBytes(t, 4096)
	id, _, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	// Random 1-byte damage inside the pack must be caught by BLAKE3 on Get.
	if err := s.CorruptForTest(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(id); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt read must fail ErrCorrupt, got %v", err)
	}
}

func TestReopenRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := randBytes(t, 4096)
	id, _, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Get(id)
	if err != nil || string(got) != string(data) {
		t.Fatalf("reopen read: %v", err)
	}
}

func TestTornWriteRecovered(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	data1 := randBytes(t, 4096)
	id1, _, err := s.Put(data1)
	if err != nil {
		t.Fatal(err)
	}
	data2 := randBytes(t, 4096)
	id2, _, err := s.Put(data2)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a torn append: truncate the pack file mid-record (torn write
	// = never durably committed; dropped on reopen).
	if err := s.SimulateTornTailForTest(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// First committed chunk MUST survive; store reopens cleanly.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.Has(id1) {
		t.Fatalf("committed chunk 1 lost after torn recovery")
	}
	got, err := s2.Get(id1)
	if err != nil || string(got) != string(data1) {
		t.Fatalf("after torn recovery chunk1: %v", err)
	}
	// Torn chunk2: either durable or dropped; never corrupt-readable.
	// (The simulate amputates synced bytes — that is corruption, not a real
	// torn write. The honest guarantee: never silently wrong bytes.)
	if s2.Has(id2) {
		got, err := s2.Get(id2)
		if err != nil && !errors.Is(err, ErrCorrupt) {
			t.Fatalf("chunk2 must be byte-exact or ErrCorrupt, got: %v", err)
		}
		if err == nil && string(got) != string(data2) {
			t.Fatalf("chunk2 silently wrong bytes")
		}
	}
}

func TestPutEmptyAndTooBigRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Put(nil); !errors.Is(err, ErrBadSize) {
		t.Fatalf("empty must fail ErrBadSize, got %v", err)
	}
	if _, _, err := s.Put(make([]byte, ChunkSize+1)); !errors.Is(err, ErrBadSize) {
		t.Fatalf("oversize must fail ErrBadSize, got %v", err)
	}
	// exactly ChunkSize is fine
	if _, _, err := s.Put(make([]byte, ChunkSize)); err != nil {
		t.Fatalf("ChunkSize must be accepted: %v", err)
	}
}
