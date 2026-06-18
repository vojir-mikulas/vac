package backup

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// VerifySchedStore is the persistence slice the verification scheduler reads.
type VerifySchedStore interface {
	ListEnabledBackupConfigs(ctx context.Context) ([]store.BackupConfig, error)
	LatestVerification(ctx context.Context, configID string) (store.BackupVerification, error)
}

// verifyRunner is the verification primitive — satisfied by *Verifier.
type verifyRunner interface {
	CanVerify(cfg store.BackupConfig) bool
	VerifyOnce(ctx context.Context, cfg store.BackupConfig) error
}

// VerifyScheduler periodically confirms backups are restorable. On each tick it
// re-verifies every enabled, verifiable config whose last verification is older
// than `interval` (or has never run). Modeled on the backup Scheduler; gated at
// main.go wiring so it adds zero footprint when managed services are off.
type VerifyScheduler struct {
	store    VerifySchedStore
	verifier verifyRunner
	logger   *slog.Logger
	now      func() time.Time
	// interval is the re-verify cadence; tick is how often to scan for due configs.
	interval time.Duration
	tick     time.Duration
}

// NewVerifyScheduler wires the scheduler with a weekly re-verify cadence.
func NewVerifyScheduler(s VerifySchedStore, v *Verifier, logger *slog.Logger) *VerifyScheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &VerifyScheduler{
		store:    s,
		verifier: v,
		logger:   logger,
		now:      time.Now,
		interval: 7 * 24 * time.Hour,
		tick:     6 * time.Hour,
	}
}

// Run blocks until ctx is cancelled, scanning every tick.
func (s *VerifyScheduler) Run(ctx context.Context) {
	for {
		s.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.tick):
		}
	}
}

// sweep verifies every enabled config whose last verification is missing or
// stale. A still-running verification is left alone (VerifyOnce guards anyway).
func (s *VerifyScheduler) sweep(ctx context.Context) {
	configs, err := s.store.ListEnabledBackupConfigs(ctx)
	if err != nil {
		s.logger.Warn("backup: verify sweep: load configs", "err", err)
		return
	}
	now := s.now()
	for _, cfg := range configs {
		if !s.verifier.CanVerify(cfg) {
			continue // custom command — can't build a restorability check
		}
		if last, err := s.store.LatestVerification(ctx, cfg.ID); err == nil {
			if last.Status == "running" || now.Sub(last.StartedAt) < s.interval {
				continue // recently checked (or in flight) — not due yet
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			s.logger.Warn("backup: verify sweep: latest verification", "config", cfg.ID, "err", err)
			continue
		}
		if err := s.verifier.VerifyOnce(ctx, cfg); err != nil {
			// VerifyOnce records + notifies; ErrNoArtifact just means nothing to
			// verify yet. Log the rest for the operator trail.
			if !errors.Is(err, ErrNoArtifact) {
				s.logger.Warn("backup: scheduled verification failed", "config", cfg.ID, "err", err)
			}
		}
	}
}
