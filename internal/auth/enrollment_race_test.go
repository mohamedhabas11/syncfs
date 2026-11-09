package auth

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentConsumeExactlyOneSucceeds races N goroutines on one
// enrollment bundle; exactly one must succeed (atomic TryConsume).
func TestConcurrentConsumeExactlyOneSucceeds(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Put(EnrollmentRecord{NodeID: "n-race", CertFingerprint: "fp", Nonce: "n", ExpiresAt: now.Add(time.Minute)})
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Consume(store, "n-race", "fp", "n", now); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("concurrent consume of one bundle must succeed exactly once, got %d", ok)
	}
}
