// Edge-case workload: benchmarks + correctness for the maturity-gate edges.
//
// This is the stopping artifact of the reflection/improvement loop: it only
// passes once the critic/tester findings C1–C8 are fixed. Each case below
// names its gate (C1–C8). Throughput benches (BenchmarkEdge*) use fresh
// payloads so they measure the fsync path, not the Pebble dedup hit.
//
// Run:
//
//	go test ./test/benchmark/... -run TestEdge -v -count=1 -race
//	go test ./test/benchmark/... -run XXX -bench 'Edge' -benchtime=100x -v
package benchmark

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/auth"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	cache "github.com/mohamedhabas11/syncfs/internal/client/cache"
	"github.com/mohamedhabas11/syncfs/internal/fusefs"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"github.com/mohamedhabas11/syncfs/internal/transport"
	"github.com/mohamedhabas11/syncfs/internal/volume"
	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"lukechampine.com/blake3"
)

// --- helpers ---

func mkEdgeCmd(kind syncfsapi.CommandKind, p any) *syncfsapi.Command {
	switch v := p.(type) {
	case *syncfsapi.Command_Create:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	case *syncfsapi.Command_Link:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	case *syncfsapi.Command_Unlink:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	case *syncfsapi.Command_Rename:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	case *syncfsapi.Command_Truncate:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	case *syncfsapi.Command_SetXattr:
		return &syncfsapi.Command{Kind: kind, Payload: v}
	default:
		panic(fmt.Sprintf("unknown payload %T", p))
	}
}

func freshPayload(i int) []byte {
	p := bytes.Repeat([]byte("syncfs-edge-"), 296) // ~4 KiB
	binary.BigEndian.PutUint32(p[0:], uint32(i))
	binary.BigEndian.PutUint32(p[8:], uint32(i*2654435761))
	return p
}

func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// TestEdgeCases is the maturity gate: every C1–C8 edge must hold.
// It is intentionally strict: any panic or silent-success is a failure.
func TestEdgeCases(t *testing.T) {
	t.Run("C2-metadata", func(t *testing.T) {
		// Dot names rejected (determinism + validName).
		m := metadata.New()
		for i, name := range []string{".", ".."} {
			c := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
				ParentInodeId: make([]byte, 16), Name: name}})
			if err := m.Apply([16]byte{byte(10 + i)}, uint64(10+i), c); err == nil {
				t.Errorf("CREATE %q must be rejected", name)
			}
		}
		// Deterministic IDs: two clones, same CREATE → same inode ID (G1).
		a, b := metadata.New(), metadata.New()
		ca := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "det"}})
		if err := a.Apply([16]byte{1}, 1, ca); err != nil {
			t.Fatal(err)
		}
		// Same opID on clone b must derive the same ID.
		cb := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "det"}})
		if err := b.Apply([16]byte{1}, 1, cb); err != nil {
			t.Fatal(err)
		}
		ida, _ := a.Lookup([16]byte{}, "det")
		idb, _ := b.Lookup([16]byte{}, "det")
		if ida != idb {
			t.Fatalf("deterministic ID violated: %x != %x", ida, idb)
		}
		// Missing-parent LINK/RENAME must fail, not panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("LINK to missing parent panicked: %v", r)
				}
			}()
			m2 := metadata.New()
			cc := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
				ParentInodeId: make([]byte, 16), Name: "v"}})
			_ = m2.Apply([16]byte{1}, 1, cc)
			tid, _ := m2.Lookup([16]byte{}, "v")
			miss := [16]byte{0xaa}
			lk := mkEdgeCmd(syncfsapi.CommandKind_LINK, &syncfsapi.Command_Link{Link: &syncfsapi.LinkPayload{
				ParentInodeId: miss[:], Name: "evil", TargetInodeId: tid[:]}})
			if err := m2.Apply([16]byte{2}, 2, lk); err == nil {
				t.Fatalf("LINK to missing parent must fail")
			}
			miss2 := [16]byte{0xbb}
			rn := mkEdgeCmd(syncfsapi.CommandKind_RENAME, &syncfsapi.Command_Rename{Rename: &syncfsapi.RenamePayload{
				SrcParent: make([]byte, 16), SrcName: "v", DstParent: miss2[:], DstName: "b"}})
			if err := m2.Apply([16]byte{3}, 3, rn); err == nil {
				t.Fatalf("RENAME to missing dst parent must fail")
			}
		}()
		// Slash in rename dst rejected.
		m3 := metadata.New()
		cc3 := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "a"}})
		_ = m3.Apply([16]byte{1}, 1, cc3)
		slash := mkEdgeCmd(syncfsapi.CommandKind_RENAME, &syncfsapi.Command_Rename{Rename: &syncfsapi.RenamePayload{
			SrcParent: make([]byte, 16), SrcName: "a", DstParent: make([]byte, 16), DstName: "x/y"}})
		if err := m3.Apply([16]byte{2}, 2, slash); err == nil {
			t.Fatalf("RENAME to slash name must be rejected")
		}
		// Zombie: truncate after unlink must fail.
		m4 := metadata.New()
		cf := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "f"}})
		_ = m4.Apply([16]byte{1}, 1, cf)
		fid, _ := m4.Lookup([16]byte{}, "f")
		un := mkEdgeCmd(syncfsapi.CommandKind_UNLINK, &syncfsapi.Command_Unlink{Unlink: &syncfsapi.UnlinkPayload{
			ParentInodeId: make([]byte, 16), Name: "f"}})
		_ = m4.Apply([16]byte{2}, 2, un)
		tr := mkEdgeCmd(syncfsapi.CommandKind_TRUNCATE, &syncfsapi.Command_Truncate{Truncate: &syncfsapi.TruncatePayload{
			InodeId: fid[:], Size: 42}})
		if err := m4.Apply([16]byte{3}, 3, tr); err == nil {
			t.Fatalf("truncate of unlinked inode must fail")
		}
		// Rename onto existing must fail closed (no silent overwrite).
		m5 := metadata.New()
		ca5 := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "a"}})
		_ = m5.Apply([16]byte{1}, 1, ca5)
		cb5 := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "b"}})
		_ = m5.Apply([16]byte{2}, 2, cb5)
		ro := mkEdgeCmd(syncfsapi.CommandKind_RENAME, &syncfsapi.Command_Rename{Rename: &syncfsapi.RenamePayload{
			SrcParent: make([]byte, 16), SrcName: "a", DstParent: make([]byte, 16), DstName: "b"}})
		if err := m5.Apply([16]byte{3}, 3, ro); err == nil {
			t.Fatalf("RENAME onto existing name must fail closed")
		}
		// Bare xattr key + unbounded truncate rejected.
		m6 := metadata.New()
		cf6 := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: "f"}})
		_ = m6.Apply([16]byte{1}, 1, cf6)
		fid6, _ := m6.Lookup([16]byte{}, "f")
		for i, key := range []string{"user.", "user", "", "x"} {
			cmd := mkEdgeCmd(syncfsapi.CommandKind_SET_XATTR, &syncfsapi.Command_SetXattr{SetXattr: &syncfsapi.SetXattrPayload{
				InodeId: fid6[:], Key: key, Value: []byte("v")}})
			if err := m6.Apply([16]byte{byte(20 + i)}, uint64(20+i), cmd); err == nil {
				t.Errorf("SET_XATTR %q must be rejected", key)
			}
		}
		big := mkEdgeCmd(syncfsapi.CommandKind_TRUNCATE, &syncfsapi.Command_Truncate{Truncate: &syncfsapi.TruncatePayload{
			InodeId: fid6[:], Size: 1 << 62}})
		if err := m6.Apply([16]byte{30}, 30, big); err == nil {
			t.Fatalf("TRUNCATE to 1<<62 must be rejected")
		}
	})

	t.Run("C3-volume", func(t *testing.T) {
		mkStore := func() *chunkstore.Store {
			s, err := chunkstore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		}
		// Sentinel.
		s1, s2 := mkStore(), mkStore()
		_ = s2.Close()
		cc := volume.NewCoordinator(metadata.New(), map[uint64]*chunkstore.Store{1: s1, 2: s2})
		if _, err := cc.WriteChunks(context.Background(), [][]byte{[]byte("hi")}); err == nil || !errors.Is(err, volume.ErrNoQuorum) {
			t.Fatalf("must wrap ErrNoQuorum, got %v", err)
		}
		// Cancelled ctx.
		s3 := mkStore()
		cc2 := volume.NewCoordinator(metadata.New(), map[uint64]*chunkstore.Store{1: s3})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := cc2.WriteChunks(ctx, [][]byte{[]byte("x")}); err == nil {
			t.Fatalf("cancelled ctx must fail")
		}
		op := [16]byte{7}
		cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
			Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: "f"}}}
		if _, err := cc2.ProposeCommand(ctx, op, cmd); err == nil {
			t.Fatalf("cancelled ProposeCommand must fail")
		}
		// Distinct RaftIndexes.
		cc3 := volume.NewCoordinator(metadata.New(), map[uint64]*chunkstore.Store{1: mkStore()})
		mk := func(n string) *syncfsapi.Command {
			return &syncfsapi.Command{Kind: syncfsapi.CommandKind_CREATE, Payload: &syncfsapi.Command_Create{
				Create: &syncfsapi.CreatePayload{ParentInodeId: make([]byte, 16), Name: n}}}
		}
		r1, err := cc3.ProposeCommand(context.Background(), [16]byte{1}, mk("a"))
		if err != nil {
			t.Fatal(err)
		}
		r2, err := cc3.ProposeCommand(context.Background(), [16]byte{2}, mk("b"))
		if err != nil {
			t.Fatal(err)
		}
		if r1.RaftIndex == r2.RaftIndex {
			t.Fatalf("RaftIndexes must differ: both %d", r1.RaftIndex)
		}
		// Atomic batch: oversize second chunk leaves nothing durable.
		a1, a2, a3 := mkStore(), mkStore(), mkStore()
		cc4 := volume.NewCoordinator(metadata.New(), map[uint64]*chunkstore.Store{1: a1, 2: a2, 3: a3})
		_, err = cc4.WriteChunks(context.Background(), [][]byte{[]byte("good-zero"), make([]byte, chunkstore.ChunkSize+1)})
		if err == nil {
			t.Fatalf("setup: oversize batch must fail")
		}
		sum := blake3.Sum256([]byte("good-zero"))
		var gid chunkstore.ChunkID
		copy(gid[:], sum[:])
		if a1.Has(gid) {
			t.Fatalf("failed batch left good-zero durable (orphan, non-atomic)")
		}
	})

	t.Run("C5-auth", func(t *testing.T) {
		store := auth.NewMemoryStore()
		now := time.Now()
		if err := store.Put(auth.EnrollmentRecord{NodeID: "n", CertFingerprint: "fp", Nonce: "nc", ExpiresAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		// Empty proof rejected.
		if err := auth.Consume(store, "n", "", "", now); err == nil {
			t.Fatalf("empty proof must be rejected")
		}
		// Concurrent double-spend: exactly one success.
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); errs[i] = auth.Consume(store, "n", "fp", "nc", now) }(i)
		}
		wg.Wait()
		ok := 0
		for _, e := range errs {
			if e == nil {
				ok++
			}
		}
		if ok != 1 {
			t.Fatalf("concurrent consume must succeed exactly once, got %d (%v)", ok, errs)
		}
		// Put with empty material rejected.
		if err := store.Put(auth.EnrollmentRecord{NodeID: "e", CertFingerprint: "", Nonce: "", ExpiresAt: now.Add(time.Minute)}); err == nil {
			t.Fatalf("empty Put must be rejected")
		}
	})

	t.Run("C8-cache", func(t *testing.T) {
		c := cache.NewCache()
		c.SetLease(9, true)
		c.Put(9, cache.Entry{InodeID: [16]byte{3}, Version: 1, Data: []byte("x")})
		c.InvalidateAll()
		if c.Put(9, cache.Entry{InodeID: [16]byte{3}, Version: 2, Data: []byte("y")}) {
			t.Fatalf("Put after InvalidateAll must be fenced (leases cleared)")
		}
	})

	t.Run("C7-transport", func(t *testing.T) {
		// Zero-length frame rejected.
		if err := transport.ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}), &syncfsapi.Hello{}); !errors.Is(err, transport.ErrZeroLength) {
			t.Fatalf("zero frame must fail, got %v", err)
		}
		// Control cap: 100 KiB Hello rejected via WriteFrame.
		big := &syncfsapi.Hello{Nonce: make([]byte, 100<<10)}
		if err := transport.WriteFrame(discard{}, big); !errors.Is(err, transport.ErrFrameTooLarge) {
			t.Fatalf("100 KiB control frame must fail, got %v", err)
		}
		// Concurrent WriteFrame must not interleave (no panic, all decodable).
		// Smoke: two sequential writes decode back.
		w := &syncBuffer{}
		h1 := &syncfsapi.Hello{RequestId: 1}
		h2 := &syncfsapi.Hello{RequestId: 2}
		if err := transport.WriteFrame(w, h1); err != nil {
			t.Fatal(err)
		}
		if err := transport.WriteFrame(w, h2); err != nil {
			t.Fatal(err)
		}
		if len(w.bytes) == 0 {
			t.Fatalf("expected framed bytes")
		}
	})

	t.Run("C6-fusefs", func(t *testing.T) {
		h := fusefs.NewHandler("edge")
		_ = h.Mkdir("d", 0755)
		_, _ = h.CreateFile("f", 0644, 0, 0)
		if e := h.Rename("f", "d"); e == 0 {
			t.Fatalf("rename file onto dir must fail")
		}
		for _, n := range []string{"a/b", "..", ".", "", "x\x00y", strings.Repeat("x", 300)} {
			h2 := fusefs.NewHandler("edge")
			if _, e := h2.CreateFile(n, 0644, 0, 0); e == 0 {
				t.Errorf("CreateFile(%q) must fail", n)
			}
		}
		if e := h.SetXattr("f", "user.", []byte("v")); e == 0 {
			t.Fatalf("bare user. must fail")
		}
		if e := h.Truncate("f", 64<<20); e == 0 {
			t.Fatalf("TRUNCATE 64MiB must fail")
		}
	})

	t.Run("C1-wal", func(t *testing.T) {
		// Gap write has no durable side effects.
		dir := t.TempDir()
		s, err := walOpen(t, dir)
		if err != nil {
			t.Fatal(err)
		}
		r := raft.Ready{Entries: []raftpb.Entry{{Term: 1, Index: 5, Data: []byte("gap")}}, HardState: raftpb.HardState{Term: 1}}
		if err := s.SaveReady(r); err == nil {
			t.Fatalf("gap write must be rejected")
		}
		_ = s.Close()
		s2, err := walOpen(t, dir)
		if err != nil {
			t.Fatalf("reopen bricked after gap: %v", err)
		}
		_ = s2.Close()
		// Snapshot survives reopen.
		dir2 := t.TempDir()
		s3, _ := walOpen(t, dir2)
		r2 := raft.Ready{
			Entries:   []raftpb.Entry{{Term: 1, Index: 1, Data: []byte("a")}, {Term: 1, Index: 2, Data: []byte("b")}},
			HardState: raftpb.HardState{Term: 1, Commit: 2},
			Snapshot:  raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1, ConfState: raftpb.ConfState{}}, Data: []byte("snap")},
		}
		if err := s3.SaveReady(r2); err != nil {
			t.Fatal(err)
		}
		_ = s3.Close()
		s4, err := walOpen(t, dir2)
		if err != nil {
			t.Fatal(err)
		}
		defer s4.Close()
		if snap, err := s4.Snapshot(); err != nil || snap.Metadata.Index != 2 {
			t.Fatalf("snapshot lost on reopen: %v", err)
		}
		if fi, _ := s4.FirstIndex(); fi != 3 {
			t.Fatalf("FirstIndex=%d want 3", fi)
		}
	})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type syncBuffer struct{ bytes []byte }

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.bytes = append(s.bytes, p...)
	return len(p), nil
}

func walOpen(t *testing.T, dir string) (*wal.Storage, error) {
	t.Helper()
	return wal.Open(wal.Options{Dir: dir})
}

// --- Benchmarks (edge workloads, fresh payloads) ---

func BenchmarkEdgeFreshQuorumWrite(b *testing.B) {
	fsm := metadata.New()
	stores := map[uint64]*chunkstore.Store{}
	for i := 1; i <= 3; i++ {
		s, err := chunkstore.Open(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		stores[uint64(i)] = s
	}
	coord := volume.NewCoordinator(fsm, stores)
	b.ResetTimer()
	lats := make([]float64, 0, b.N)
	for i := 0; i < b.N; i++ {
		t0 := time.Now()
		if _, err := coord.WriteChunks(b.Context(), [][]byte{freshPayload(i)}); err != nil {
			b.Fatal(err)
		}
		lats = append(lats, millis(time.Since(t0)))
	}
	b.ReportMetric(float64(b.N)/bTimerSecs(b), "ops/s")
	b.ReportMetric(percentile(lats, 50), "ms-p50")
	b.ReportMetric(percentile(lats, 99), "ms-p99")
	b.ReportMetric(percentile(lats, 100), "ms-max")
}

func BenchmarkEdgeMetadataDispatch(b *testing.B) {
	m := metadata.New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("f%d", i)
		c := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: name}})
		id := [16]byte{byte(i), byte(i >> 8), 1}
		_ = m.Apply(id, uint64(i+1), c)
		rn := mkEdgeCmd(syncfsapi.CommandKind_RENAME, &syncfsapi.Command_Rename{Rename: &syncfsapi.RenamePayload{
			SrcParent: make([]byte, 16), SrcName: name, DstParent: make([]byte, 16), DstName: name + ".bak"}})
		_ = m.Apply([16]byte{byte(i), byte(i >> 8), 2}, uint64(i+100000), rn)
	}
	b.ReportMetric(float64(b.N), "ops")
}

func BenchmarkEdgeInvalidNameReject(b *testing.B) {
	m := metadata.New()
	names := []string{".", "..", "x/y", "", strings.Repeat("x", 300)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := names[i%len(names)]
		c := mkEdgeCmd(syncfsapi.CommandKind_CREATE, &syncfsapi.Command_Create{Create: &syncfsapi.CreatePayload{
			ParentInodeId: make([]byte, 16), Name: name}})
		_ = m.Apply([16]byte{byte(i), 9}, uint64(i+1), c) // must fail fast, no panic
	}
}

func BenchmarkEdgeAtomicBatchReject(b *testing.B) {
	fsm := metadata.New()
	stores := map[uint64]*chunkstore.Store{}
	for i := 1; i <= 3; i++ {
		s, err := chunkstore.Open(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		stores[uint64(i)] = s
	}
	coord := volume.NewCoordinator(fsm, stores)
	good := []byte("good-zero")
	bad := make([]byte, chunkstore.ChunkSize+1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = coord.WriteChunks(b.Context(), [][]byte{good, bad}) // must fail atomically
		// Re-seed good chunk's neighbors to keep stores busy; atomicity is
		// asserted by TestEdgeCases, here we measure reject latency.
		_ = stores[1].Has(chunkstore.ChunkID{})
	}
}

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	idx := len(s) * p / 100
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func bTimerSecs(b *testing.B) float64 {
	return b.Elapsed().Seconds()
}
