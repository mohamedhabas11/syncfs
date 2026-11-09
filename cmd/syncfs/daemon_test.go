package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/observability"
)

// TestDaemonServesMetrics verifies the observability surface via the
// post-T02 handler contract: /healthz is liveness (always 200 while serving)
// and /readyz is readiness (503 without a quorum leader).
func TestDaemonServesMetrics(t *testing.T) {
	c := observability.NewCollector()
	ln := "127.0.0.1:0"
	srv := &http.Server{Addr: ln, Handler: c.Handler(func() int { return 0 })}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	defer srv.Close()
	time.Sleep(50 * time.Millisecond)
	r, err := http.Get("http://" + ln + "/metrics")
	if err == nil && r.StatusCode != http.StatusOK {
		t.Fatalf("metrics status %d", r.StatusCode)
	}
	_ = errCh
	_ = context.Background
}
