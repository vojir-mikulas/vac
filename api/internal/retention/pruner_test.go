package retention_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/retention"
)

type fakeStore struct {
	calls      atomic.Int64
	last       time.Time
	rmCalls    atomic.Int64
	rmLast     time.Time
	auditCalls atomic.Int64
	auditLast  time.Time
	trimCalls  atomic.Int64
	trimKeep   int
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

func (f *fakeStore) DeleteAuditLogOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.auditCalls.Add(1)
	f.auditLast = cutoff
	return 5, nil
}

func (f *fakeStore) ListRuntimeLogServices(_ context.Context) ([]struct{ AppID, ServiceName string }, error) {
	return []struct{ AppID, ServiceName string }{{AppID: "app-1", ServiceName: "web"}}, nil
}

func (f *fakeStore) TrimRuntimeLogsToRingBuffer(_ context.Context, _, _ string, keepN int) (int64, error) {
	f.trimCalls.Add(1)
	f.trimKeep = keepN
	return 3, nil
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

func TestPruneOnce_ComputesAuditCutoffFromActivityDays(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, retention.Config{ActivityDays: 30}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatalf("PruneOnce: %v", err)
	}
	if s.auditCalls.Load() != 1 {
		t.Errorf("audit delete calls = %d, want 1", s.auditCalls.Load())
	}
	diff := time.Since(s.auditLast)
	if diff < (30*24*time.Hour-time.Minute) || diff > (30*24*time.Hour+time.Minute) {
		t.Errorf("audit cutoff diff = %v, want ~30 days", diff)
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
