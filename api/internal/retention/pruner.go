// Package retention runs a nightly goroutine that deletes log rows beyond
// the configured retention window. Build logs are permanent (kept with the
// deployment record), runtime logs are not.
package retention

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// PruneStore is the slice of *store.Store the pruner writes against.
type PruneStore interface {
	DeleteRuntimeLogsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteRequestMetricsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteAuditLogOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteSecurityEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteJobRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	ListRuntimeLogServices(ctx context.Context) ([]struct{ AppID, ServiceName string }, error)
	TrimRuntimeLogsToRingBuffer(ctx context.Context, appID, serviceName string, keepN int) (int64, error)
	// Image prune (P2.3) + deployment retention (P2.4).
	ListServiceProjects(ctx context.Context) ([]struct{ Slug, ServiceName string }, error)
	PruneDeployments(ctx context.Context, keepN int) (int64, error)
}

// ImagePruner lists and removes per-service docker images. Satisfied by
// *dockercli.Compose. nil disables the image-prune pass (e.g. tests, or a host
// without a docker client wired).
type ImagePruner interface {
	ListImages(ctx context.Context, projectName, serviceName string) ([]dockercli.Image, error)
	RemoveImage(ctx context.Context, id string) error
	BuildCachePrune(ctx context.Context, maxBytes int64) error
}

// Config carries the retention windows. RuntimeDays governs runtime_logs;
// RequestMetrics governs the request_metrics rolling window (default 24h).
type Config struct {
	RuntimeDays int
	// RequestMetrics is the retention for the request-rate window.
	RequestMetrics time.Duration
	// ActivityDays governs the audit_log (the activity feed). Default 30.
	ActivityDays int
	// RingBuffer caps runtime_logs per (app, service) — the mvp ring buffer.
	// The live follower trims continuously; this catches stopped-app services
	// whose follower isn't running. Default 10000.
	RingBuffer int
	// ImageKeepCount is how many most-recent images to keep per service when the
	// image-prune pass runs. <=0 disables image pruning. Default 3.
	ImageKeepCount int
	// DeploymentKeepCount is how many most-recent deployments to keep per app.
	// <=0 disables deployment retention. Default 20.
	DeploymentKeepCount int
	// BuildCacheMaxBytes caps the BuildKit layer cache after the nightly pass;
	// the cache is trimmed to this ceiling so layer reuse across deploys stays
	// reliable without unbounded disk growth. <=0 disables the pass (the
	// VAC_BUILD_CACHE=false escape hatch resolves here), leaving the cache to
	// Docker's own GC.
	BuildCacheMaxBytes int64
	// Hour of day (0-23) the prune runs in time.Local. Default 3 (03:00).
	HourOfDay int
}

// Pruner is the background scheduler.
type Pruner struct {
	store  PruneStore
	images ImagePruner // nil → image-prune pass skipped
	cfg    Config
	logger *slog.Logger
	now    func() time.Time // injectable for tests
}

// New returns a Pruner with the given store + config. images may be nil to skip
// the per-service image-prune pass.
func New(s PruneStore, images ImagePruner, cfg Config, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.RuntimeDays <= 0 {
		cfg.RuntimeDays = 7
	}
	if cfg.RequestMetrics <= 0 {
		cfg.RequestMetrics = 24 * time.Hour
	}
	if cfg.ActivityDays <= 0 {
		cfg.ActivityDays = 30
	}
	if cfg.RingBuffer <= 0 {
		cfg.RingBuffer = 10000
	}
	if cfg.ImageKeepCount <= 0 {
		cfg.ImageKeepCount = 3
	}
	if cfg.DeploymentKeepCount <= 0 {
		cfg.DeploymentKeepCount = 20
	}
	if cfg.HourOfDay < 0 || cfg.HourOfDay > 23 {
		cfg.HourOfDay = 3
	}
	return &Pruner{
		store:  s,
		images: images,
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

	rmCutoff := p.now().Add(-p.cfg.RequestMetrics)
	rn, err := p.store.DeleteRequestMetricsOlderThan(ctx, rmCutoff)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned request metrics", "deleted", rn, "cutoff", rmCutoff.Format(time.RFC3339))

	auditCutoff := p.now().Add(-time.Duration(p.cfg.ActivityDays) * 24 * time.Hour)
	an, err := p.store.DeleteAuditLogOlderThan(ctx, auditCutoff)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned audit log", "deleted", an, "cutoff", auditCutoff.Format(time.RFC3339))

	// Security events (diverted unauthenticated attempts) share the activity
	// window — they're the same feed's overflow, kept just as long.
	sn, err := p.store.DeleteSecurityEventsOlderThan(ctx, auditCutoff)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned security events", "deleted", sn, "cutoff", auditCutoff.Format(time.RFC3339))

	// User-cron run history shares the activity window — it's the same kind of
	// operational history as the audit log, and without this a frequently-running
	// job grows job_runs without bound.
	jn, err := p.store.DeleteJobRunsOlderThan(ctx, auditCutoff)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned job runs", "deleted", jn, "cutoff", auditCutoff.Format(time.RFC3339))

	// Ring-buffer cap per (app, service) — catches services whose live
	// follower isn't running to trim them continuously.
	pairs, err := p.store.ListRuntimeLogServices(ctx)
	if err != nil {
		return err
	}
	var trimmed int64
	for _, pr := range pairs {
		n, err := p.store.TrimRuntimeLogsToRingBuffer(ctx, pr.AppID, pr.ServiceName, p.cfg.RingBuffer)
		if err != nil {
			p.logger.Warn("retention: ring-buffer trim failed", "app", pr.AppID, "service", pr.ServiceName, "err", err)
			continue
		}
		trimmed += n
	}
	p.logger.Info("retention: ring-buffer trim", "deleted", trimmed, "keep_per_service", p.cfg.RingBuffer)

	// Deployment retention: trim history beyond the rollback window.
	dn, err := p.store.PruneDeployments(ctx, p.cfg.DeploymentKeepCount)
	if err != nil {
		return err
	}
	p.logger.Info("retention: pruned deployments", "deleted", dn, "keep_per_app", p.cfg.DeploymentKeepCount)

	// Image prune: keep only the newest ImageKeepCount images per service. Done
	// last and best-effort — a docker hiccup here must not fail the DB passes.
	p.pruneImages(ctx)

	// Build-cache prune: bound the BuildKit layer cache. Best-effort and kept in
	// the same nightly pass so cache GC and image GC stay together; failures are
	// logged, never propagated.
	p.pruneBuildCache(ctx)
	return nil
}

// pruneBuildCache trims the BuildKit layer cache to cfg.BuildCacheMaxBytes.
// Skipped when no docker client is wired or the cap is non-positive (the
// VAC_BUILD_CACHE=false escape hatch). Best-effort: a failure is logged and
// swallowed so it never fails the DB passes that ran before it.
func (p *Pruner) pruneBuildCache(ctx context.Context) {
	if p.images == nil || p.cfg.BuildCacheMaxBytes <= 0 {
		return
	}
	if err := p.images.BuildCachePrune(ctx, p.cfg.BuildCacheMaxBytes); err != nil {
		p.logger.Warn("retention: build-cache prune failed", "max_bytes", p.cfg.BuildCacheMaxBytes, "err", err)
		return
	}
	p.logger.Info("retention: pruned build cache", "max_bytes", p.cfg.BuildCacheMaxBytes)
}

// pruneImages removes all but the newest ImageKeepCount images per
// (compose project, service). Best-effort: a list/remove failure for one
// service is logged and skipped, never propagated. "image in use" removals
// (the live image) are expected and ignored.
func (p *Pruner) pruneImages(ctx context.Context) {
	if p.images == nil {
		return
	}
	pairs, err := p.store.ListServiceProjects(ctx)
	if err != nil {
		p.logger.Warn("retention: list service projects failed", "err", err)
		return
	}
	var removed int
	for _, pr := range pairs {
		project := "vac-" + pr.Slug // mirrors deploy.composeProject
		imgs, err := p.images.ListImages(ctx, project, pr.ServiceName)
		if err != nil {
			p.logger.Warn("retention: list images failed", "project", project, "service", pr.ServiceName, "err", err)
			continue
		}
		for _, id := range staleImageIDs(imgs, p.cfg.ImageKeepCount) {
			if err := p.images.RemoveImage(ctx, id); err != nil {
				// In-use (current) image, or another transient — expected; skip.
				p.logger.Debug("retention: remove image skipped", "id", id, "err", err)
				continue
			}
			removed++
		}
	}
	p.logger.Info("retention: pruned images", "removed", removed, "keep_per_service", p.cfg.ImageKeepCount)
}

// staleImageIDs returns the image IDs to remove: everything except the newest
// keepN by CreatedAt. Docker's `images` output is newest-first already, but we
// sort defensively on the parsed CreatedAt (format "2006-01-02 15:04:05 -0700 MST")
// and fall back to the listed order when a timestamp won't parse.
func staleImageIDs(imgs []dockercli.Image, keepN int) []string {
	if keepN < 0 {
		keepN = 0
	}
	if len(imgs) <= keepN {
		return nil
	}
	sorted := make([]dockercli.Image, len(imgs))
	copy(sorted, imgs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return imageCreatedAt(sorted[i].CreatedAt).After(imageCreatedAt(sorted[j].CreatedAt))
	})
	var ids []string
	for _, img := range sorted[keepN:] {
		if strings.TrimSpace(img.ID) != "" {
			ids = append(ids, img.ID)
		}
	}
	return ids
}

// imageCreatedAt parses docker's CreatedAt string; unparseable values sort
// oldest (zero time) so a malformed timestamp never shields an image from prune.
func imageCreatedAt(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func timeUntilNext(now time.Time, hour int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
