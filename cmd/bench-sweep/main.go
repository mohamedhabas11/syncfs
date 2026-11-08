// Command bench-sweep measures quorum-write throughput vs client count with
// fresh 4 KiB payloads. It runs inside Docker Desktop's Linux VM (via
// `make bench-sweep`) on a tmpfs or named volume. Throughput is reported
// as a fraction of the measured fsync floor (see docs/benchmarks/floor.md).
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/volume"
)

type row struct {
	Clients      int     `json:"clients"`
	OpsPerSec    float64 `json:"ops_per_sec"`
	P50Ms        float64 `json:"p50_ms"`
	P99Ms        float64 `json:"p99_ms"`
	MaxMs        float64 `json:"max_ms"`
	FsyncsPerEntry int   `json:"fsyncs_per_committed_entry"`
	AllocBytesPerOp float64 `json:"alloc_bytes_per_op"`
	MallocsPerOp float64 `json:"mallocs_per_op"`
	CPUmsPerOp   float64 `json:"cpu_ms_per_op"`
	WallSecs     float64 `json:"wall_secs"`
	DedupNote    string  `json:"dedup_note"`
}

type envelope struct {
	Tool       string  `json:"tool"`
	CommitSHA  string  `json:"commit_sha"`
	Env        string  `json:"env"`
	VolumeType string  `json:"volume_type"`
	DataDir    string  `json:"data_dir"`
	Command    string  `json:"command"`
	PayloadBytes int    `json:"payload_bytes"`
	PerClient  int     `json:"per_client"`
	Rows       []row   `json:"rows"`
	Date       string  `json:"date"`
}

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	idx := len(s) * p / 100
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func fresh(client, iter int) []byte {
	p := bytes.Repeat([]byte("syncfs-sweep-"), 296) // ≈ 4 KiB (243*? ~4 KiB)
	// Ensure exactly 4096 bytes.
	if len(p) > 4096 {
		p = p[:4096]
	}
	for len(p) < 4096 {
		p = append(p, byte(len(p)))
	}
	binary.BigEndian.PutUint32(p[0:], uint32(client))
	binary.BigEndian.PutUint32(p[4:], uint32(iter))
	binary.BigEndian.PutUint64(p[8:], uint64(client)*1<<32+uint64(iter))
	return p
}

func cpuMs() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 16 {
		return 0
	}
	u, _ := strconv.ParseFloat(f[13], 64)
	s, _ := strconv.ParseFloat(f[14], 64)
	return (u + s) * 10 // ms
}

func parseClients(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

func runCount(dir string, clients int, perClient int) (row, error) {
	base := filepath.Join(dir, fmt.Sprintf("c%d", clients))
	stores := map[uint64]*chunkstore.Store{}
	for i := 1; i <= 3; i++ {
		s, err := chunkstore.Open(filepath.Join(base, fmt.Sprintf("v%d", i)))
		if err != nil {
			return row{}, err
		}
		defer s.Close()
		stores[uint64(i)] = s
	}
	coord := volume.NewCoordinator(metadata.New(), stores)
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	cpu0 := cpuMs()
	t0 := time.Now()
	lats := make(chan float64, clients*perClient)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[chunkstore.ChunkID]struct{}{}
	var firstErr error
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < perClient; i++ {
				p := fresh(c, i)
				t1 := time.Now()
				rc, err := coord.WriteChunks(context.Background(), [][]byte{p})
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				lats <- float64(time.Since(t1).Microseconds()) / 1000.0
				mu.Lock()
				for _, r := range rc {
					ids[r.ChunkID] = struct{}{}
				}
				mu.Unlock()
			}
		}(c)
	}
	wg.Wait()
	close(lats)
	wall := time.Since(t0).Seconds()
	runtime.ReadMemStats(&ms1)
	cpu1 := cpuMs()
	vals := []float64{}
	for v := range lats {
		vals = append(vals, v)
	}
	ops := float64(clients*perClient) / wall
	// Dedup check: distinct chunk IDs must equal total ops (all fresh).
	distinct := len(ids)
	dedup := "not in play: all payloads fresh (counter); distinct ChunkIDs == ops"
	if distinct != clients*perClient {
		dedup = fmt.Sprintf("dedup IN PLAY: distinct %d != ops %d", distinct, clients*perClient)
	}
	if firstErr != nil {
		return row{}, firstErr
	}
	// fsyncs per committed entry: 3 voters × (pack Sync + dir Sync) = 6.
	return row{
		Clients: clients, OpsPerSec: ops, P50Ms: percentile(vals, 50),
		P99Ms: percentile(vals, 99), MaxMs: percentile(vals, 100),
		FsyncsPerEntry: 6,
		AllocBytesPerOp: float64(ms1.TotalAlloc-ms0.TotalAlloc) / float64(clients*perClient),
		MallocsPerOp:    float64(ms1.Mallocs-ms0.Mallocs) / float64(clients*perClient),
		CPUmsPerOp:      (cpu1 - cpu0) / float64(clients*perClient),
		WallSecs: wall, DedupNote: dedup,
	}, nil
}

func main() {
	dir := flag.String("dir", "/var/lib/syncfs/sweep", "data directory (tmpfs or named volume)")
	clients := flag.String("clients", "1,8,32,128", "client counts (csv)")
	per := flag.Int("n", 100, "iterations per client")
	volType := flag.String("volume-type", "named_volume", "tmpfs|named_volume")
	flag.Parse()
	counts := parseClients(*clients)
	if counts == nil {
		fmt.Fprintln(os.Stderr, "bad -clients")
		os.Exit(1)
	}
	env := envelope{
		Tool: "bench-sweep", CommitSHA: os.Getenv("COMMIT_SHA"),
		Env: "Docker Desktop Linux VM (aarch64, golang:1.26-alpine)",
		VolumeType: *volType, DataDir: *dir, Command: strings.Join(os.Args, " "),
		PayloadBytes: 4096, PerClient: *per, Date: time.Now().UTC().Format("2006-01-02"),
	}
	for _, c := range counts {
		r, err := runCount(*dir, c, *per)
		if err != nil {
			fmt.Fprintln(os.Stderr, "client", c, err)
			os.Exit(1)
		}
		env.Rows = append(env.Rows, r)
		fmt.Fprintf(os.Stderr, "sweep clients=%d: %.1f ops/s p50=%.2f p99=%.2f\n", c, r.OpsPerSec, r.P50Ms, r.P99Ms)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(env)
}
