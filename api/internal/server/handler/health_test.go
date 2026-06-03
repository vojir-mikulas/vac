package handler

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The whole point of the cache is to bound how often /health forks
// `docker version`. Verify a burst of calls within the TTL probes exactly once.
func TestCachedDockerProbe_CachesWithinTTL(t *testing.T) {
	var calls int
	c := newCachedDockerProbe(time.Minute)
	c.probe = func(context.Context) dockerStatus {
		calls++
		return dockerStatusOK
	}

	for i := 0; i < 50; i++ {
		if got := c.status(context.Background()); got != dockerStatusOK {
			t.Fatalf("call %d: got %v, want OK", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("probe called %d times within TTL, want 1", calls)
	}
}

// Once the TTL lapses the probe must run again so the signal can recover.
func TestCachedDockerProbe_RefreshesAfterTTL(t *testing.T) {
	var calls int
	c := newCachedDockerProbe(time.Millisecond)
	c.probe = func(context.Context) dockerStatus {
		calls++
		return dockerStatusOK
	}

	c.status(context.Background())
	time.Sleep(5 * time.Millisecond)
	c.status(context.Background())
	if calls != 2 {
		t.Fatalf("probe called %d times across TTL boundary, want 2", calls)
	}
}

// Concurrent cold hits must collapse to a single fork (stampede protection):
// the mutex is held across the probe so latecomers reuse the freshly-cached value.
func TestCachedDockerProbe_ConcurrentColdHitsProbeOnce(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := newCachedDockerProbe(time.Minute)
	c.probe = func(context.Context) dockerStatus {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(2 * time.Millisecond) // simulate the fork latency
		return dockerStatusOK
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.status(context.Background())
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("probe called %d times under concurrent cold load, want 1", calls)
	}
}
