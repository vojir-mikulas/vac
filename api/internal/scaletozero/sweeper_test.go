package scaletozero

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeSuspender records which apps the sweeper asked to suspend.
type fakeSuspender struct {
	mu        sync.Mutex
	suspended []string
	done      chan struct{}
}

func (s *fakeSuspender) Suspend(_ context.Context, app store.App, _ time.Time) error {
	s.mu.Lock()
	s.suspended = append(s.suspended, app.ID)
	s.mu.Unlock()
	if s.done != nil {
		s.done <- struct{}{}
	}
	return nil
}

func (s *fakeSuspender) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.suspended...)
}

func newSweeperAt(st Store, sus Suspender, now time.Time) *Sweeper {
	sw := NewSweeper(st, sus, time.Minute, 15*time.Minute, nil)
	sw.now = func() time.Time { return now }
	return sw
}

func TestSweeper_SuspendsIdlePastWindow(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	// Idle 20m ago > 15m window + grace → eligible.
	st.idleApps = []store.App{{ID: "a1", Slug: "blog"}}
	st.lastTraffic["a1"] = now.Add(-20 * time.Minute)

	sus := &fakeSuspender{done: make(chan struct{}, 1)}
	sw := newSweeperAt(st, sus, now)
	sw.tick(context.Background())
	<-sus.done // suspend runs in its own goroutine

	if got := sus.calls(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("suspended = %+v, want [a1]", got)
	}
}

func TestSweeper_SkipsRecentTraffic(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	// Traffic 5m ago < 15m window → not eligible.
	st.idleApps = []store.App{{ID: "a1", Slug: "blog"}}
	st.lastTraffic["a1"] = now.Add(-5 * time.Minute)

	sus := &fakeSuspender{}
	sw := newSweeperAt(st, sus, now)
	sw.tick(context.Background())

	if got := sus.calls(); len(got) != 0 {
		t.Errorf("suspended = %+v, want none", got)
	}
}

func TestSweeper_PerAppTimeoutOverride(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	five := 5
	// 10m idle: past the per-app 5m override (even though under the 15m default).
	st.idleApps = []store.App{{ID: "a1", Slug: "blog", IdleTimeoutMinutes: &five}}
	st.lastTraffic["a1"] = now.Add(-10 * time.Minute)

	sus := &fakeSuspender{done: make(chan struct{}, 1)}
	sw := newSweeperAt(st, sus, now)
	sw.tick(context.Background())
	<-sus.done

	if got := sus.calls(); len(got) != 1 {
		t.Errorf("per-app override should have suspended, got %+v", got)
	}
}

func TestSweeper_NoTrafficAnchorsOnUpdatedAt(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	// No request rows at all (zero time). A freshly-updated app (1m ago) must NOT
	// be suspended; only one updated long ago is eligible.
	st.idleApps = []store.App{
		{ID: "fresh", Slug: "fresh", UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "stale", Slug: "stale", UpdatedAt: now.Add(-2 * time.Hour)},
	}

	sus := &fakeSuspender{done: make(chan struct{}, 1)}
	sw := newSweeperAt(st, sus, now)
	sw.tick(context.Background())
	<-sus.done

	got := sus.calls()
	if len(got) != 1 || got[0] != "stale" {
		t.Errorf("suspended = %+v, want [stale]", got)
	}
}
