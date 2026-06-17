// Package preview implements per-branch ephemeral preview environments
// (docs/plans/preview-deployments.md). A preview is deliberately just an app —
// is_preview=true, parent_app_id set, a derived slug {parent}-{branch} — so it
// reuses the entire existing deploy pipeline, router, and teardown path
// unchanged. This package owns only the lifecycle that wasn't there before:
// create-or-redeploy on a matching branch push (EnsurePreview), reap on
// branch-delete / manual teardown / TTL (Teardown + the Expirer), and the hard
// concurrency cap the single box needs.
package preview

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrCapReached is returned by EnsurePreview when the instance already has
// MaxPreviews previews and the branch is a new one. The single box has one
// finite RAM/disk budget, so a new preview is refused (with a notification)
// rather than risking an OOM that takes production down (decision #5).
var ErrCapReached = errors.New("preview: instance is at its preview limit")

// ErrNotPreview guards Teardown against being pointed at a non-preview app — the
// teardown path is `compose down -v` + DeleteApp, which would be catastrophic on
// a production app.
var ErrNotPreview = errors.New("preview: app is not a preview")

// ErrSlugUnavailable means the derived {parent}-{branch} slug can't be formed as
// a valid DNS label (the parent slug leaves no room, or it collides with an
// existing app).
var ErrSlugUnavailable = errors.New("preview: could not derive a usable preview slug")

// maxSlugLen is a valid DNS label / app-slug ceiling (mirrors handler.maxSlugLen).
const maxSlugLen = 63

// Store is the slice of *store.Store the preview lifecycle needs.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	CreatePreviewApp(ctx context.Context, parent store.App, slug, branch string) (store.App, error)
	GetPreviewByParentAndBranch(ctx context.Context, parentID, branch string) (store.App, error)
	ListExpiredPreviews(ctx context.Context, olderThan time.Duration) ([]store.App, error)
	CountPreviews(ctx context.Context) (int, error)
	TouchPreviewPush(ctx context.Context, id string) error
	HasActiveDeployment(ctx context.Context, appID string) (bool, error)
	CreateDeployment(ctx context.Context, appID, triggeredBy string, rolledBackFrom *string) (store.Deployment, error)
	ActiveDeploymentIDsForApp(ctx context.Context, appID string) ([]string, error)
	DeleteApp(ctx context.Context, id string) error
}

// Enqueuer enqueues a deployment for the worker. *deploy.Worker satisfies it.
type Enqueuer interface {
	Enqueue(deploymentID string) error
}

// Canceller interrupts an in-flight deploy and refreshes the live queue panel.
// *deploy.Worker satisfies it. Optional (nil disables interrupt-on-teardown).
type Canceller interface {
	Cancel(deploymentID string) bool
	NotifyChanged()
}

// StackController is the docker-compose surface used to tear a preview's stack
// (and its volumes) down. *dockercli.Compose satisfies it. Optional.
type StackController interface {
	Down(ctx context.Context, projectName string, removeVolumes bool) error
}

// Router keeps Caddy routing in step: Teardown drops a preview's routes + edge
// attachments, Reconcile prunes the now-orphaned auto-host routes after delete.
// *proxy.Manager satisfies it. Optional.
type Router interface {
	Teardown(ctx context.Context, appID string) error
	Reconcile(ctx context.Context) error
}

// Notifier fires the cap-reached alert. *notify.Dispatcher satisfies it. Optional.
type Notifier interface {
	PreviewCapReached(appName, appID, branch string, max int)
}

// Config parameterises the preview lifecycle.
type Config struct {
	// MaxPreviews is the global hard cap (VAC_MAX_PREVIEWS). <=0 disables previews
	// entirely (every EnsurePreview of a new branch refuses).
	MaxPreviews int
	// TTL is the idle window after which a preview with no new push is reaped
	// (VAC_PREVIEW_TTL). <=0 disables auto-expiry.
	TTL time.Duration
	// SweepInterval is how often the expirer scans for expired previews. Defaults
	// to 15m.
	SweepInterval time.Duration
}

// Service is the preview lifecycle orchestrator.
type Service struct {
	store     Store
	enqueuer  Enqueuer
	canceller Canceller
	ctrl      StackController
	router    Router
	notifier  Notifier
	cfg       Config
	logger    *slog.Logger
}

// New wires a Service. enqueuer is required (a preview that can't deploy is
// pointless); the rest are optional and nil-guarded.
func New(s Store, enqueuer Enqueuer, canceller Canceller, ctrl StackController, router Router, notifier Notifier, cfg Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 15 * time.Minute
	}
	return &Service{
		store:     s,
		enqueuer:  enqueuer,
		canceller: canceller,
		ctrl:      ctrl,
		router:    router,
		notifier:  notifier,
		cfg:       cfg,
		logger:    logger,
	}
}

// MaxPreviews reports the configured cap (for the UI budget line).
func (s *Service) MaxPreviews() int { return s.cfg.MaxPreviews }

// EnsurePreview is the create-or-redeploy entry point for a push to a preview
// branch. If a preview for (parent, branch) already exists it is redeployed
// (coalesced against an in-flight build); otherwise — subject to the cap — a new
// preview app is created and deployed. Returns ErrCapReached when a *new*
// preview would exceed MaxPreviews (an existing preview always redeploys).
func (s *Service) EnsurePreview(ctx context.Context, parentID, branch string) error {
	parent, err := s.store.GetApp(ctx, parentID)
	if err != nil {
		return err
	}

	existing, err := s.store.GetPreviewByParentAndBranch(ctx, parentID, branch)
	switch {
	case err == nil:
		// Redeploy: reset the TTL clock, then enqueue (coalesced if a build is
		// already running for this preview).
		if err := s.store.TouchPreviewPush(ctx, existing.ID); err != nil {
			s.logger.Warn("preview: touch push time", "preview", existing.ID, "err", err)
		}
		return s.enqueue(ctx, existing.ID)
	case !errors.Is(err, store.ErrNotFound):
		return err
	}

	// New preview — enforce the cap before creating any infra.
	if s.cfg.MaxPreviews <= 0 {
		return ErrCapReached
	}
	count, err := s.store.CountPreviews(ctx)
	if err != nil {
		return err
	}
	if count >= s.cfg.MaxPreviews {
		if s.notifier != nil {
			s.notifier.PreviewCapReached(parent.Name, parent.ID, branch, s.cfg.MaxPreviews)
		}
		s.logger.Info("preview: cap reached, refusing", "parent", parent.Slug, "branch", branch, "max", s.cfg.MaxPreviews)
		return ErrCapReached
	}

	slug := DeriveSlug(parent.Slug, branch)
	if slug == "" {
		return ErrSlugUnavailable
	}
	pv, err := s.store.CreatePreviewApp(ctx, parent, slug, branch)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ErrSlugUnavailable
		}
		return err
	}
	s.logger.Info("preview: created", "parent", parent.Slug, "branch", branch, "slug", pv.Slug)
	return s.enqueue(ctx, pv.ID)
}

// enqueue creates + enqueues a preview deployment, coalescing against an
// in-flight build (mirrors the webhook handler's active-deploy guard).
func (s *Service) enqueue(ctx context.Context, previewID string) error {
	if active, _ := s.store.HasActiveDeployment(ctx, previewID); active {
		return nil // coalesce: a build is already in flight for this preview
	}
	d, err := s.store.CreateDeployment(ctx, previewID, store.TriggeredPreview, nil)
	if err != nil {
		if errors.Is(err, store.ErrActiveDeploymentExists) {
			return nil // lost the race; coalesce
		}
		return err
	}
	return s.enqueuer.Enqueue(d.ID)
}

// Teardown reaps a preview: it interrupts any in-flight deploy, brings the stack
// down with its volumes, drops its routes + edge attachments, deletes the app
// row (cascading services/deployments/env/domains/managed_dbs), and reconciles
// so the now-orphaned auto-host routes are pruned. This is exactly the app-delete
// path; it refuses to run against a non-preview app.
func (s *Service) Teardown(ctx context.Context, previewID string) error {
	app, err := s.store.GetApp(ctx, previewID)
	if err != nil {
		return err
	}
	if !app.IsPreview {
		return ErrNotPreview
	}

	// Interrupt any in-flight deploy first so the worker frees its pool slot
	// instead of grinding the pipeline against torn-down infra. Best-effort.
	if s.canceller != nil {
		if dids, derr := s.store.ActiveDeploymentIDsForApp(ctx, previewID); derr != nil {
			s.logger.Warn("preview: list active deployments", "preview", previewID, "err", derr)
		} else {
			for _, did := range dids {
				s.canceller.Cancel(did)
			}
		}
	}
	// Stop + remove containers and named volumes (best-effort — a stuck stack
	// must not block the row delete).
	if s.ctrl != nil {
		if err := s.ctrl.Down(ctx, "vac-"+app.Slug, true); err != nil {
			s.logger.Warn("preview: compose down", "preview", app.Slug, "err", err)
		}
	}
	// Drop routes + edge attachments before the cascade removes the rows we'd
	// need to find them.
	if s.router != nil {
		if err := s.router.Teardown(ctx, previewID); err != nil {
			s.logger.Warn("preview: proxy teardown", "preview", app.Slug, "err", err)
		}
	}
	if err := s.store.DeleteApp(ctx, previewID); err != nil {
		return err
	}
	// Prune the now-orphaned vac-auto-* routes.
	if s.router != nil {
		if err := s.router.Reconcile(ctx); err != nil {
			s.logger.Warn("preview: reconcile after teardown", "preview", app.Slug, "err", err)
		}
	}
	if s.canceller != nil {
		s.canceller.NotifyChanged()
	}
	s.logger.Info("preview: torn down", "slug", app.Slug)
	return nil
}

// TeardownByBranch reaps the preview for (parent, branch) if one exists. It is
// the branch-delete / PR-close webhook signal's entry point; a branch that never
// had a preview returns store.ErrNotFound, which the caller treats as a no-op.
func (s *Service) TeardownByBranch(ctx context.Context, parentID, branch string) error {
	pv, err := s.store.GetPreviewByParentAndBranch(ctx, parentID, branch)
	if err != nil {
		return err
	}
	return s.Teardown(ctx, pv.ID)
}

// RunExpirer is the long-lived TTL sweeper goroutine (mirrors certcheck /
// diskusage.Collector). Every SweepInterval it reaps previews idle past TTL. It
// is the backstop for any teardown the webhook misses (a deleted branch with no
// delivery still expires). A non-positive TTL disables it entirely. Exits when
// ctx is cancelled.
func (s *Service) RunExpirer(ctx context.Context) {
	if s.cfg.TTL <= 0 {
		s.logger.Info("preview: TTL expiry disabled (no VAC_PREVIEW_TTL)")
		return
	}
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

// sweepOnce reaps every preview currently past its TTL.
func (s *Service) sweepOnce(ctx context.Context) {
	expired, err := s.store.ListExpiredPreviews(ctx, s.cfg.TTL)
	if err != nil {
		s.logger.Warn("preview: list expired", "err", err)
		return
	}
	for _, p := range expired {
		s.logger.Info("preview: expired, tearing down", "slug", p.Slug, "ttl", s.cfg.TTL)
		if err := s.Teardown(ctx, p.ID); err != nil {
			s.logger.Warn("preview: expire teardown", "slug", p.Slug, "err", err)
		}
	}
}

// DeriveSlug builds a preview app slug from the parent slug and branch:
// {parentSlug}-{branchSlug}, branch slugified and truncated so the whole slug is
// a valid DNS label (<= 63 chars, no trailing hyphen). Returns "" when no usable
// slug can be formed (the parent slug leaves no room for a branch segment).
func DeriveSlug(parentSlug, branch string) string {
	bs := slugify(branch)
	if bs == "" {
		return ""
	}
	// Reserve parentSlug + "-"; the rest is the branch budget.
	budget := maxSlugLen - len(parentSlug) - 1
	if budget <= 0 {
		return ""
	}
	if len(bs) > budget {
		bs = strings.TrimRight(bs[:budget], "-")
	}
	if bs == "" {
		return ""
	}
	return parentSlug + "-" + bs
}

// slugify lower-cases and reduces a free-form branch name to a kebab-case DNS
// label fragment: non-alnum runs collapse to a single hyphen, leading/trailing
// hyphens are stripped (mirrors handler.deriveSlug).
func slugify(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastHyphen := true // suppress leading hyphen
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
