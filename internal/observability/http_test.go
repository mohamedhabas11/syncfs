package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpositionHelpAndTypes(t *testing.T) {
	c := NewCollector()
	c.SetFromStats(Stats{
		FollowerLag: 7, ChunkChecksumFailures: 3, CheckpointIndex: 500,
		AmbiguousOps: 2,
	})
	out := c.Prometheus()
	for _, want := range []string{
		"# HELP syncfs_follower_lag",
		"syncfs_follower_lag 7",
		"syncfs_chunk_checksum_failures_total 3",
		"# TYPE syncfs_chunk_checksum_failures_total counter",
		"# TYPE syncfs_ambiguous_operations_total counter",
		"syncfs_checkpoint 500",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHandlerMetricsHealthz(t *testing.T) {
	c := NewCollector()
	c.SetFromStats(Stats{LeaderQuorum: 2, CommittedIndex: 9})
	srv := httptest.NewServer(c.Handler(func() int { return 2 }))
	defer srv.Close()
	r, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("metrics status %d", r.StatusCode)
	}
	r2, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("healthz liveness status %d want 200", r2.StatusCode)
	}
	r2b, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if r2b.StatusCode != http.StatusOK {
		t.Fatalf("readyz with quorum status %d want 200", r2b.StatusCode)
	}
	srv2 := httptest.NewServer(c.Handler(func() int { return 0 }))
	defer srv2.Close()
	// Liveness stays 200 without quorum; readiness is 503.
	r3, err := http.Get(srv2.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("healthz liveness without quorum status %d want 200", r3.StatusCode)
	}
	r4, err := http.Get(srv2.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if r4.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz no-quorum status %d want 503", r4.StatusCode)
	}
}
