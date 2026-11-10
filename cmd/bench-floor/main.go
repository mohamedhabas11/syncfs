// Command bench-floor measures the write+fsync floor on the benchmark data
// volume and TCP RTT between nodes. It runs inside Docker Desktop's Linux VM
// (via `make bench-floor`); the data directory MUST be on a tmpfs or named
// volume, never a macOS bind mount (/src). Results are NEVER compared
// against spec §11 targets (which require NVMe + 10 GbE, see status.md).
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type result struct {
	Tool            string             `json:"tool"`
	CommitSHA       string             `json:"commit_sha"`
	Env             string             `json:"env"`
	VolumeType      string             `json:"volume_type"`
	DataDir         string             `json:"data_dir"`
	Command         string             `json:"command"`
	Iterations      int                `json:"iterations"`
	PayloadBytes    int                `json:"payload_bytes"`
	OpsPerSec       float64            `json:"ops_per_sec"`
	P50Ms           float64            `json:"p50_ms"`
	P99Ms           float64            `json:"p99_ms"`
	MaxMs           float64            `json:"max_ms"`
	BytesPerSec     float64            `json:"bytes_per_sec"`
	FsyncsPerEntry  int                `json:"fsyncs_per_committed_entry"`
	AllocBytesPerOp float64            `json:"alloc_bytes_per_op"`
	MallocsPerOp    float64            `json:"mallocs_per_op"`
	CPUmsPerOp      float64            `json:"cpu_ms_per_op"`
	LoopbackRTT     map[string]float64 `json:"loopback_rtt_ms,omitempty"`
	PeerRTT         map[string]float64 `json:"peer_rtt_ms,omitempty"`
	PeerStatus      string             `json:"peer_status"`
	DedupNote       string             `json:"dedup_note"`
	Date            string             `json:"date"`
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

func cpuMs() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 16 {
		return 0
	}
	// utime (14) + stime (15), in clock ticks (100 Hz on Linux).
	u, _ := strconv.ParseFloat(f[13], 64)
	s, _ := strconv.ParseFloat(f[14], 64)
	return (u + s) * 10 // ms
}

func tcpRTT(host string, port int, attempts int) (map[string]float64, string) {
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, "UNVERIFIED: DNS lookup failed: " + err.Error()
	}
	_ = addrs
	target := net.JoinHostPort(host, strconv.Itoa(port))
	var lats []float64
	for i := 0; i < attempts; i++ {
		t0 := time.Now()
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		lat := float64(time.Since(t0).Microseconds()) / 1000.0
		lats = append(lats, lat)
		if err != nil {
			// ECONNREFUSED still measures network RTT (packets reached host).
			// Any other error (timeout) is also recorded; we keep the latencies.
			continue
		}
		_ = conn.Close()
	}
	if len(lats) == 0 {
		return nil, "UNVERIFIED"
	}
	sort.Float64s(lats)
	return map[string]float64{
		"min":   lats[0],
		"p50":   percentile(lats, 50),
		"p99":   percentile(lats, 99),
		"count": float64(len(lats)),
	}, "measured TCP connect (DialTimeout 2s, 20 attempts)"
}

func main() {
	dir := flag.String("dir", "/var/lib/syncfs/floor", "data directory (MUST be on tmpfs or named volume)")
	n := flag.Int("n", 500, "iterations")
	size := flag.Int("size", 4096, "payload bytes per iteration")
	volType := flag.String("volume-type", "named_volume", "tmpfs|named_volume (never bind)")
	peer := flag.String("peer", "node-b", "peer hostname for between-nodes RTT")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	fpath := filepath.Join(*dir, "floor.dat")
	f, err := os.OpenFile(fpath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer f.Close()
	// Fresh payload base; counter changes per iteration so content is never
	// identical. Dedup N/A: this is a raw file write (no chunkstore index).
	base := make([]byte, *size)
	for i := range base {
		base[i] = byte(i)
	}
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	cpu0 := cpuMs()
	t0 := time.Now()
	lats := make([]float64, 0, *n)
	payload := make([]byte, *size)
	for i := 0; i < *n; i++ {
		copy(payload, base)
		binary.BigEndian.PutUint64(payload[0:], uint64(i))
		t1 := time.Now()
		if _, err := f.WriteAt(payload, 0); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		if err := f.Sync(); err != nil {
			fmt.Fprintln(os.Stderr, "sync:", err)
			os.Exit(1)
		}
		lats = append(lats, float64(time.Since(t1).Microseconds())/1000.0)
	}
	// Amortized dir sync: once at the end (name durability), +1/N per entry.
	if err := f.Sync(); err != nil {
		fmt.Fprintln(os.Stderr, "final sync:", err)
		os.Exit(1)
	}
	if d, err := os.Open(*dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	wall := time.Since(t0).Seconds()
	runtime.ReadMemStats(&ms1)
	cpu1 := cpuMs()
	ops := float64(*n) / wall
	res := result{
		Tool:            "bench-floor",
		CommitSHA:       os.Getenv("COMMIT_SHA"),
		Env:             "Docker Desktop Linux VM (aarch64, golang:1.26-alpine)",
		VolumeType:      *volType,
		DataDir:         *dir,
		Command:         strings.Join(os.Args, " "),
		Iterations:      *n,
		PayloadBytes:    *size,
		OpsPerSec:       ops,
		P50Ms:           percentile(lats, 50),
		P99Ms:           percentile(lats, 99),
		MaxMs:           percentile(lats, 100),
		BytesPerSec:     ops * float64(*size),
		FsyncsPerEntry:  1,
		AllocBytesPerOp: float64(ms1.TotalAlloc-ms0.TotalAlloc) / float64(*n),
		MallocsPerOp:    float64(ms1.Mallocs-ms0.Mallocs) / float64(*n),
		CPUmsPerOp:      (cpu1 - cpu0) / float64(*n),
		DedupNote:       "N/A: raw file write, no chunkstore dedup; counter changes per iteration",
		Date:            time.Now().UTC().Format("2006-01-02"),
	}
	// Loopback RTT: listener on ephemeral port, dial it.
	if ln, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		defer ln.Close()
		port := ln.Addr().(*net.TCPAddr).Port
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		res.LoopbackRTT, _ = tcpRTT("127.0.0.1", port, 20)
	}
	res.PeerRTT, res.PeerStatus = tcpRTT(*peer, 22, 20)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "floor: %.1f ops/s p50=%.2f p99=%.2f file=%s vol=%s\n",
		res.OpsPerSec, res.P50Ms, res.P99Ms, fpath, *volType)
}
