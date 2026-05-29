package retention_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/retention"
)

type fakeStore struct {
	calls   atomic.Int64
	last    time.Time
	rmCalls atomic.Int64
	rmLast  time.Time
}

func (f *fakeStore) DeleteRuntimeLogsOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.calls.Add(1)
	f.last = cutoff
	return 42, nil
}

func (f *fakeStore) DeleteRequestMetricsOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.rmCalls.Add(1)
	f.rmLast = cutoff
	return 7, nil
}

func TestPruneOnce_ComputesCutoffFromRuntimeDays(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, retention.Config{RuntimeDays: 7}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatalf("PruneOnce: %v", err)
	}
	if s.calls.Load() != 1 {
		t.Errorf("delete calls = %d, want 1", s.calls.Load())
	}
	// Cutoff should be approximately 7 days ago.
	diff := time.Since(s.last)
	if diff < (7*24*time.Hour-time.Minute) || diff > (7*24*time.Hour+time.Minute) {
		t.Errorf("cutoff diff = %v, want ~7 days", diff)
	}
}

func TestPruneOnce_DefaultsTo7Days(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, retention.Config{}, nil) // empty config
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	diff := time.Since(s.last)
	if diff < (7*24*time.Hour-time.Minute) || diff > (7*24*time.Hour+time.Minute) {
		t.Errorf("default cutoff diff = %v, want ~7 days", diff)
	}
}
