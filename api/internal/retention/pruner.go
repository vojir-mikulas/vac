// Package retention runs a nightly goroutine that deletes log rows beyond
// the configured retention window. Build logs are permanent (kept with the
// deployment record), runtime logs are not.
package retention

import (
	"context"
	"log/slog"
	"time"
)

// PruneStore is the slice of *store.Store the pruner writes against.
type PruneStore interface {
	DeleteRuntimeLogsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Config carries the retention windows. RuntimeDays governs runtime_logs.
type Config struct {
	RuntimeDays int
	// Hour of day (0-23) the prune runs in time.Local. Default 3 (03:00).
	HourOfDay int
}

// Pruner is the background scheduler.
type Pruner struct {
	store  PruneStore
	cfg    Config
	logger *slog.Logger
	now    func() time.Time // injectable for tests
}

// New returns a Pruner with the given store + config.
func New(s PruneStore, cfg Config, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.RuntimeDays <= 0 {
		cfg.RuntimeDays = 7
	}
	if cfg.HourOfDay < 0 || cfg.HourOfDay > 23 {
		cfg.HourOfDay = 3
	}
	return &Pruner{
		store:  s,
		cfg:    cfg,
		logger: logger,
		now:    time.Now,
	}
}

// Run sleeps until the next scheduled prune time, runs PruneOnce, then
// loops. Exits when ctx is cancelled.
func (p *Pruner) Run(ctx context.Context) {
	for {
		wait := timeUntilNext(p.now(), p.cfg.HourOfDay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := p.PruneOnce(ctx); err != nil {
			p.logger.Warn("retention: prune failed", "err", err)
		}
	}
}

// PruneOnce executes the prune immediately. Exposed for tests and for a
// future on-demand admin endpoint.
func (p *Pruner) PruneOnce(ctx context.Context) error {
	cutoff := p.now().Add(-time.Duration(p.cfg.RuntimeDays) * 24 * time.Hour)
	n, err := p.store.DeleteRuntimeLogsOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned runtime logs", "deleted", n, "cutoff", cutoff.Format(time.RFC3339))
	return nil
}

func timeUntilNext(now time.Time, hour int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
