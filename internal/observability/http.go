package observability

import "net/http"

// Handler returns an http.Handler serving Prometheus exposition at
// /metrics, liveness at /healthz (200 while the process serves), and
// readiness at /readyz (200 only when quorumFn() > 0, i.e. a live leader
// with quorum; 503 otherwise). Liveness must never be derived from
// configured quorum; readiness must never hide quorum loss.
func (c *Collector) Handler(quorumFn func() int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(c.Prometheus()))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if quorumFn == nil || quorumFn() > 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no-quorum"))
	})
	return mux
}
