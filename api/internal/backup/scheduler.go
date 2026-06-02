package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ConfigLister loads the scheduler's working set (every enabled config).
type ConfigLister interface {
	ListEnabledBackupConfigs(ctx context.Context) ([]store.BackupConfig, error)
}

// Runner executes a single backup — satisfied by *Engine.
type Runner interface {
	RunOnce(ctx context.Context, cfg store.BackupConfig) error
}

// Scheduler is the pruner-pattern goroutine: load enabled configs, compute the
// soonest next-due time, sleep to it, run everything now due, repeat. Gated by
// VAC_MANAGED_SERVICES at the main.go wiring so it adds zero footprint when off.
type Scheduler struct {
	store  ConfigLister
	engine Runner
	logger *slog.Logger
	now    func() time.Time
	// idle is how long to wait before re-checking when no configs exist (so a
	// newly-added config is picked up without a restart).
	idle time.Duration
}

// NewScheduler wires the scheduler.
func NewScheduler(s ConfigLister, engine Runner, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:  s,
		engine: engine,
		logger: logger,
		now:    time.Now,
		idle:   time.Hour,
	}
}

// Run blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		now := s.now()
		configs, err := s.store.ListEnabledBackupConfigs(ctx)
		if err != nil {
			s.logger.Warn("backup: load configs", "err", err)
		}

		// Snapshot each config's next-due time before sleeping; after waking, run
		// the ones whose due time has passed.
		type scheduled struct {
			cfg store.BackupConfig
			at  time.Time
		}
		items := make([]scheduled, 0, len(configs))
		var soonest time.Time
		for _, c := range configs {
			at := nextOccurrence(now, c.Frequency, c.HourOfDay, c.DayOfWeek)
			items = append(items, scheduled{cfg: c, at: at})
			if soonest.IsZero() || at.Before(soonest) {
				soonest = at
			}
		}

		wait := s.idle
		if !soonest.IsZero() {
			if d := soonest.Sub(now); d > 0 {
				wait = d
			} else {
				wait = 0
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		after := s.now()
		for _, it := range items {
			if it.at.After(after) {
				continue
			}
			if err := s.engine.RunOnce(ctx, it.cfg); err != nil {
				// RunOnce already records + notifies; log for the operator trail.
				s.logger.Warn("backup: scheduled run failed", "config", it.cfg.ID, "err", err)
			}
		}
	}
}

// nextOccurrence computes the next scheduled time at or after now for a config.
// daily → today (or tomorrow) at hour; weekly → the next dayOfWeek at hour.
// hour is clamped to 0-23; an out-of-range or nil day_of_week for weekly falls
// back to daily semantics.
func nextOccurrence(now time.Time, freq string, hour int, dow *int) time.Time {
	if hour < 0 || hour > 23 {
		hour = 3
	}
	base := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())

	if freq == "weekly" && dow != nil {
		target := ((*dow % 7) + 7) % 7
		daysAhead := (target - int(now.Weekday()) + 7) % 7
		cand := base.AddDate(0, 0, daysAhead)
		if !cand.After(now) {
			cand = cand.AddDate(0, 0, 7)
		}
		return cand
	}

	if !base.After(now) {
		base = base.AddDate(0, 0, 1)
	}
	return base
}
