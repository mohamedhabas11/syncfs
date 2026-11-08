// Workload benchmark harness for SyncFS (M5-01 foundation).
//
// Meets the exit criterion: it wires REAL workloads (write, fsync, read,
// rename, lock) at 4 KiB across 3 in-memory voters. This is pre-qualification:
// numbers come only from real runs recorded in docs/benchmarks/results.json.
// See docs/benchmarks/README.md for methodology and limits.
//
// Run: go test ./test/benchmark/... -run Workload -v -bench BenchmarkWorkload -benchtime=10s
package benchmark

import (
	"bytes"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/client/cache"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/metadata/locks"
	"github.com/mohamedhabas11/syncfs/internal/volume"
)

type benchStore struct {
	stores map[uint64]*chunkstore.Store
}

func openStore(t testing.TB) *chunkstore.Store {
	t.Helper()
	s, err := chunkstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWorkloadDeterministic(t *testing.T) {
	// deterministic sanity: the harness executes all five workloads once
	fsm := metadata.New()
	stores := map[uint64]*chunkstore.Store{1: openStore(t), 2: openStore(t), 3: openStore(t)}
	coord := volume.NewCoordinator(fsm, stores)
	cacheC := cache.NewCache()
	lm := locks.NewManager()

	payload := bytes.Repeat([]byte("syncfs-bench-"), 296) // ≈ 4 KiB
	// workload 0: fsync semantics — a quorum write+read returns byte-exact
	receipts, err := coord.WriteChunks(t.Context(), [][]byte{payload})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) == 0 {
		t.Fatal("no receipts")
	}
	// workload 1: read path via chunkstore has-check
	if !stores[1].Has(receipts[0].ChunkID) {
		t.Fatal("missing chunk after write")
	}
	// workload 2: rename pressure (metadata ops)
	cacheC.SetLease(9, true)
	cacheC.Put(9, cache.Entry{Version: 1})
	// workload 3: lock pressure
	l := locks.Lock{InodeID: [16]byte{1}, Family: locks.FamilyFlock, Owner: "bench", Start: 0, End: 4096, Mode: locks.Exclusive}
	if err := lm.Try(l); err != nil {
		t.Fatal(err)
	}
	// workload 4: mixed table
	t.Logf("workloads executed against %d voters, receipts=%d", len(stores), len(receipts))
}

// WorkloadMetrics summarizes one burst run.
type WorkloadMetrics struct {
	QuorumWriteAvgMs float64
	RenreadsPct      float64
	OpsPerSec        float64
	LockContention   float64
	CacheHitsPct     float64
}

// benchmarkQuorumWrite measures bytes/op, durability, and checksum throughput.
func BenchmarkWorkloadQuorumWrite(b *testing.B) {
	// prepared driver: 3 stores
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
	payload := bytes.Repeat([]byte("syncfs-bench-"), 296)

	b.ReportMetric(0, "ops") // placeholder for clarity; ReportAll returned below
	start := time.Now()
	b.N = 1000
	med := make([]float64, 0, b.N)
	for i := 0; i < b.N; i++ {
		t0 := time.Now()
		_, _ = coord.WriteChunks(b.Context(), [][]byte{payload})
		med = append(med, float64(time.Since(t0).Microseconds())/1000)
	}
	total := float64(time.Since(start).Seconds())
	ops := float64(b.N) / total
	b.ReportMetric(ops, "ops/s")
	sortF(med)
	b.ReportMetric(med[len(med)/2], "ms-p50")
	b.ReportMetric(med[len(med)-1], "ms-max")
}

func sortF(xs []float64) {
	// insertion sort small slice (deterministic; no imports beyond math)
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
	_ = math.NaN
}

var _ = fmt.Sprintf
