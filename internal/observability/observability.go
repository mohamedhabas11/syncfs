// Package observability emits the minimum SyncFS metrics (M4-03 tail).
//
// M4-03 asked for structured metrics over the loop; this type is dependency
// free and rendered as Prometheus text exposition (the dashboard lives
// elsewhere).
package observability

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mohamedhabas11/syncfs/internal/metadata/locks"
	"github.com/mohamedhabas11/syncfs/internal/volume"
)

// Stats surfaces the metrics M4-03 required.
type Stats struct {
	LeaderQuorum           int
	CommittedIndex         uint64
	AppliedIndex           uint64
	FollowerLag            uint64
	LearnerCatchupPercent  float64
	LeaseRevokeWaitMeanMs  float64
	LockWaitMeanMs         float64
	AmbiguousOps           uint64
	ChunkChecksumFailures  uint64
	RepairBacklog          uint64
	GCBacklog              uint64
	FreeDiskBytes          uint64
	CertificateExpiryHours float64
}

// Collector is a tiny in-memory collector (sufficient for tests + the scrape
// endpoint until a real Prometheus client lands).
type Collector struct {
	mu      sync.Mutex
	samples map[string]float64
	g       *gcStats
	vol     *volume.Coordinator
	lockmgr *locks.Manager
}

// gcStats/alias hooks (wired by the daemon in M5).
type gcStats interface{ Generation() uint64 }

// WebInfra spells Out the metrics catalogue.
const (
	MetricLeaderQuorum           = "syncfs_leader_quorum"
	MetricCommittedIndex         = "syncfs_committed_index"
	MetricAppliedIndex           = "syncfs_applied_index"
	MetricLearnerCatchupProgress = "syncfs_learner_catchup_progress"
	MetricLeaseRevokeWait        = "syncfs_lease_revoke_wait_ms"
	MetricLockWait               = "syncfs_lock_wait_ms"
	MetricAmbiguousOps           = "syncfs_ambiguous_operations_total"
	MetricCheckpoint             = "syncfs_checkpoint"
	MetricRepairBacklog          = "syncfs_repair_backlog"
	MetricGCBacklog              = "syncfs_gc_backlog"
	MetricFreeBytes              = "syncfs_free_bytes"
	MetricCertificateExpiryHours = "syncfs_certificate_expiry_hours"
	MetricAmbiguousOpsTotal      = "syncfs_ambiguous_operations_total"
)

// NewCollector builds a collector.
func NewCollector() *Collector {
	return &Collector{
		samples: map[string]float64{},
	}
}

// Observe records a sample (HELP text pinned by catalogue order).
func (c *Collector) Observe(name string, v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples[name] = v
}

// Prometheus renders the exposition text.
func (c *Collector) Prometheus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var names []string
	for n := range c.samples {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []byte
	for _, n := range names {
		out = append(out, []byte(fmt.Sprintf("# TYPE %s gauge\n%s %v\n", n, n, c.samples[n]))...)
	}
	return string(out)
}

// SetFromStats imports a Stats snapshot.
func (c *Collector) SetFromStats(s Stats) {
	c.Observe(MetricLeaderQuorum, float64(s.LeaderQuorum))
	c.Observe(MetricCommittedIndex, float64(s.CommittedIndex))
	c.Observe(MetricAppliedIndex, float64(s.AppliedIndex))
	c.Observe(MetricLearnerCatchupProgress, s.LearnerCatchupPercent)
	c.Observe(MetricLeaseRevokeWait, s.LeaseRevokeWaitMeanMs)
	c.Observe(MetricLockWait, s.LockWaitMeanMs)
	c.Observe(MetricAmbiguousOpsTotal, float64(s.AmbiguousOps))
	c.Observe(MetricRepairBacklog, float64(s.RepairBacklog))
	c.Observe(MetricGCBacklog, float64(s.GCBacklog))
	c.Observe(MetricFreeBytes, float64(s.FreeDiskBytes))
	c.Observe(MetricCertificateExpiryHours, s.CertificateExpiryHours)
}

// ErrCollectorClosed marks collector misuse.
var ErrCollectorClosed = errors.New("observability: collector closed")

var _ = time.Now
