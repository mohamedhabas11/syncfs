package observability

import "net/http"

// Handler returns an http.Handler serving Prometheus exposition at
// /metrics and readiness at /healthz. Health is 200 when quorum > 0,
// 503 otherwise (caller maps Stats.LeaderQuorum via quorumFn).
func (c *Collector) Handler(quorumFn func() int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(c.Prometheus()))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if quorumFn == nil || quorumFn() > 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no-quorum"))
	})
	return mux
}
