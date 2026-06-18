package deploy

import (
	"context"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/webhook"
)

// WindowStore is the slice of *store.Store the deploy-window sweeper drives.
type WindowStore interface {
	ListScheduledDeployments(ctx context.Context) ([]store.ScheduledDeploy, error)
	ReleaseScheduledDeployment(ctx context.Context, id string) error
}

// WindowEnqueuer hands a released deployment to the worker pool. Satisfied by
// *Worker.
type WindowEnqueuer interface {
	Enqueue(deploymentID string) error
}

// WindowSweeper releases deployments parked outside their app's deploy window
// (maintenance-mode-and-deploy-gates.md, Phase 3). A push that arrives outside
// every window is stored as `scheduled`; this goroutine wakes every interval,
// and for each parked deploy whose window is now open, flips it to `queued` and
// enqueues it. Mirrors the deploy reaper: one cheap, indexed query per tick, and
// nothing to do (a fast no-op) when nothing is parked.
type WindowSweeper struct {
	store    WindowStore
	enqueue  WindowEnqueuer
	logger   *slog.Logger
	now      func() time.Time
	interval time.Duration
}

// NewWindowSweeper wires the sweeper. interval defaults to one minute — deploy
// windows have minute granularity, so a parked deploy releases within ~a minute
// of its window opening.
func NewWindowSweeper(s WindowStore, enqueue WindowEnqueuer, logger *slog.Logger) *WindowSweeper {
	if logger == nil {
		logger = slog.Default()
	}
	return &WindowSweeper{
		store:    s,
		enqueue:  enqueue,
		logger:   logger,
		now:      time.Now,
		interval: time.Minute,
	}
}

// Run blocks until ctx is cancelled, sweeping once per interval (and once
// immediately on start so a deploy parked just before a restart isn't stranded).
func (s *WindowSweeper) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

// sweep releases every parked deploy whose window is currently open.
func (s *WindowSweeper) sweep(ctx context.Context) {
	parked, err := s.store.ListScheduledDeployments(ctx)
	if err != nil {
		s.logger.Warn("deploy window: list scheduled", "err", err)
		return
	}
	now := s.now()
	for _, d := range parked {
		windows, err := webhook.ParseWindows(d.DeployWindow)
		if err != nil {
			s.logger.Warn("deploy window: parse", "app", d.AppID, "err", err)
			continue
		}
		if !webhook.Allows(now, windows) {
			continue
		}
		if err := s.store.ReleaseScheduledDeployment(ctx, d.DeploymentID); err != nil {
			// ErrNotFound = a concurrent release/cancel already settled it; skip.
			s.logger.Debug("deploy window: release", "deployment", d.DeploymentID, "err", err)
			continue
		}
		if err := s.enqueue.Enqueue(d.DeploymentID); err != nil {
			s.logger.Warn("deploy window: enqueue released deploy", "deployment", d.DeploymentID, "err", err)
			continue
		}
		s.logger.Info("deploy window: released parked deploy", "deployment", d.DeploymentID, "app", d.AppID)
	}
}
