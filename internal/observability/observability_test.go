package observability

import (
	"strings"
	"testing"
	"time"
)

func TestPrometheusRendersCatalogue(t *testing.T) {
	c := NewCollector()
	c.SetFromStats(Stats{LeaderQuorum: 3, CommittedIndex: 99, AppliedIndex: 98, FreeDiskBytes: 12345})
	end := c.Prometheus()
	for _, want := range []string{
		"# TYPE syncfs_leader_quorum gauge",
		"syncfs_leader_quorum 3",
		"syncfs_committed_index 99",
		"syncfs_applied_index 98",
		"syncfs_free_bytes 12345",
	} {
		if !strings.Contains(end, want) {
			t.Fatalf("missing %q in exposition:\n%s", want, end)
		}
	}
}

func TestObserveOverrides(t *testing.T) {
	c := NewCollector()
	c.Observe(MetricAmbiguousOpsTotal, 5)
	c.Observe(MetricAmbiguousOpsTotal, 2)
	if !strings.Contains(c.Prometheus(), fmtInt(2)) {
		t.Fatalf("observe must overwrite")
	}
	tick := time.Now()
	_ = tick
}

func fmtInt(v float64) string {
	return "syncfs_ambiguous_operations_total " + string(rune('0'+int(v)))
}
