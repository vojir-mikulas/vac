package scaletozero

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Suspender is the slice of *Waker the sweeper calls, so tests can substitute a
// fake. decidedAt is the sweep tick the idleness judgment was based on; the waker
// re-validates against it to avoid suspending an app woken since.
type Suspender interface {
	Suspend(ctx context.Context, app store.App, decidedAt time.Time) error
}

// Sweeper periodically suspends opted-in apps idle past their timeout. It copies
// jobs.Scheduler's shape — one sleeping goroutine — and main.go starts it only
// when the feature is on AND ≥1 app has opted in, so idle cost is zero
// otherwise.
type Sweeper struct {
	store    Store
	waker    Suspender
	logger   *slog.Logger
	now      func() time.Time
	interval time.Duration
	timeout  time.Duration // instance-default idle window (per-app override wins)
	grace    time.Duration // slack for the ~10s request_metrics bucket-flush lag
}

// NewSweeper wires a Sweeper. interval and timeout come from config; a
// non-positive value falls back to a safe default.
func NewSweeper(s Store, waker Suspender, interval, timeout time.Duration, logger *slog.Logger) *Sweeper {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return &Sweeper{
		store:    s,
		waker:    waker,
		logger:   logger,
		now:      time.Now,
		interval: interval,
		timeout:  timeout,
		// Slack covering request_metrics' bucket-start anchoring (MAX(bucket_ts) is
		// the start of a 10s window) plus one ~10s flush interval, so an app hit
		// seconds ago isn't suspended on a stale read.
		grace: 30 * time.Second,
	}
}

// Run blocks until ctx is cancelled, scanning every interval.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.logger.Info("scaletozero: sweeper started", "interval", s.interval, "timeout", s.timeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick lists the eligible candidates and suspends those idle past their window.
func (s *Sweeper) tick(ctx context.Context) {
	apps, err := s.store.ListIdleSuspendApps(ctx)
	if err != nil {
		s.logger.Warn("scaletozero: list idle-suspend apps", "err", err)
		return
	}
	now := s.now()
	for _, app := range apps {
		idle, err := s.store.LastTrafficSince(ctx, app.ID)
		if err != nil {
			s.logger.Warn("scaletozero: last traffic", "app", app.Slug, "err", err)
			continue
		}
		// No request rows in the retained window (never served, or all aged out):
		// anchor the idle clock on the app's last update (≈ last deploy) so a
		// freshly-deployed app that simply hasn't been hit yet isn't suspended
		// instantly.
		ref := idle
		if ref.IsZero() {
			ref = app.UpdatedAt
		}
		if now.Sub(ref) < s.window(app)+s.grace {
			continue
		}
		if !idle.IsZero() {
			_ = s.store.SetLastTrafficAt(ctx, app.ID, idle)
		}
		// Suspend in its own goroutine so a slow docker stop doesn't stall the
		// sweep; the waker's in-flight guard dedups overlapping ticks and
		// re-validates against `now` so a wake since this decision wins.
		go func(a store.App) {
			// Recover here, not just in the sweeper loop: this goroutine runs
			// outside main.go's superviseDaemon frame, so an un-recovered panic in
			// Suspend would crash the whole vac-api process.
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("scaletozero: suspend panicked",
						"app", a.Slug, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			if err := s.waker.Suspend(ctx, a, now); err != nil {
				s.logger.Warn("scaletozero: suspend", "app", a.Slug, "err", err)
			}
		}(app)
	}
}

// window is the app's effective idle timeout: its per-app override when set,
// otherwise the instance default.
func (s *Sweeper) window(app store.App) time.Duration {
	if app.IdleTimeoutMinutes != nil && *app.IdleTimeoutMinutes > 0 {
		return time.Duration(*app.IdleTimeoutMinutes) * time.Minute
	}
	return s.timeout
}
