// Package scaletozero implements idle-suspend + wake-on-request
// (docs/plans/scale-to-zero.md): a sweeper stops opted-in apps idle past their
// timeout and swaps their routes for wake routes; a waker restarts the stack on
// the next request and holds the client on the existing health gate. The whole
// subsystem is opt-in (VAC_IDLE_SUSPEND + a per-app flag) and its idle cost is
// one sleeping goroutine, exactly like jobs.Scheduler.
package scaletozero

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrWaking is returned by Wake to a caller that lost the in-flight race — a
// concurrent request already triggered the start. The handler serves the waking
// page instead of waiting on a second start.
var ErrWaking = errors.New("scaletozero: wake already in progress")

// Service/app status values the waker writes. They mirror deploy's constants
// (the Go side owns these strings; there's no DB CHECK) — duplicated here to
// keep scaletozero decoupled from the deploy package.
const (
	serviceStatusRunning = "running"
	serviceStatusStopped = "stopped"
)

// Store is the persistence slice the waker and sweeper share.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
	UpdateServiceStatus(ctx context.Context, appID, name, status string, exitCode *int) error
	SetAppSuspended(ctx context.Context, id string, suspended bool) error
	SetLastTrafficAt(ctx context.Context, id string, ts time.Time) error
	LastTrafficSince(ctx context.Context, appID string) (time.Time, error)
	ListIdleSuspendApps(ctx context.Context) ([]store.App, error)
}

// Compose is the slice of *dockercli.Compose the waker drives — whole-stack
// stop/start use an empty service name.
type Compose interface {
	Stop(ctx context.Context, projectName, service string) error
	Start(ctx context.Context, projectName, service string) error
}

// Proxy is the slice of *proxy.Manager the waker drives.
type Proxy interface {
	InstallWakeRoutes(ctx context.Context, appID string) error
	Sync(ctx context.Context, appID string) error
	WaitHealthy(ctx context.Context, appID string) error
}

// Waker owns the suspend and wake transitions. A single per-app in-flight guard
// (copied from jobs.Scheduler's overlap guard) ensures a wake and a suspend
// can't race and that a thundering herd of concurrent requests triggers exactly
// one docker start.
type Waker struct {
	store  Store
	docker Compose
	proxy  Proxy
	logger *slog.Logger
	now    func() time.Time // injected for testing

	mu       sync.Mutex
	inFlight map[string]struct{}
}

// NewWaker wires a Waker.
func NewWaker(s Store, docker Compose, p Proxy, logger *slog.Logger) *Waker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Waker{
		store:    s,
		docker:   docker,
		proxy:    p,
		logger:   logger,
		now:      time.Now,
		inFlight: make(map[string]struct{}),
	}
}

// composeProject is the docker compose project name VAC uses for every app —
// must match deploy.composeProject.
func composeProject(slug string) string { return "vac-" + slug }

// acquire marks an app in-flight, returning false if a transition (wake or
// suspend) is already running for it.
func (w *Waker) acquire(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.inFlight[id]; ok {
		return false
	}
	w.inFlight[id] = struct{}{}
	return true
}

func (w *Waker) release(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inFlight, id)
}

// Suspend stops an idle app's stack and swaps its routes for wake routes. Used
// by the sweeper; decidedAt is the sweep tick that judged the app idle. Order:
// stop → mark suspended → swap routes (+ detach edge). Marking suspended before
// the route swap means a Caddy restart in the gap still re-installs wake routes
// via Reconcile. A no-op (returns nil) when a transition is already in flight for
// the app — the shared guard also blocks a concurrent wake from racing.
func (w *Waker) Suspend(ctx context.Context, app store.App, decidedAt time.Time) error {
	if !w.acquire(app.ID) {
		return nil
	}
	defer w.release(app.ID)

	// Re-validate under the guard: a wake (or a deploy) may have run between the
	// sweeper's listing and now, outside the guard's protection. A successful wake
	// clears Suspended and stamps LastTrafficAt; a deploy clears Suspended. Either
	// means the app is no longer idle, so back off rather than stop a live stack.
	fresh, err := w.store.GetApp(ctx, app.ID)
	if err != nil {
		return err
	}
	if fresh.Suspended {
		return nil
	}
	if fresh.LastTrafficAt != nil && fresh.LastTrafficAt.After(decidedAt) {
		return nil
	}

	project := composeProject(app.Slug)
	// stop, not down: volumes + named containers persist, so the wake restart is
	// fast (scale-to-zero decision #3).
	if err := w.docker.Stop(ctx, project, ""); err != nil {
		return fmt.Errorf("suspend %s: stop stack: %w", app.Slug, err)
	}
	if err := w.store.SetAppSuspended(ctx, app.ID, true); err != nil {
		return fmt.Errorf("suspend %s: mark suspended: %w", app.Slug, err)
	}
	w.markServices(ctx, app.ID, serviceStatusStopped)
	if err := w.proxy.InstallWakeRoutes(ctx, app.ID); err != nil {
		return fmt.Errorf("suspend %s: install wake routes: %w", app.Slug, err)
	}
	w.logger.Info("scaletozero: suspended idle app", "app", app.Slug)
	return nil
}

// Wake starts a suspended app's stack, re-installs its real routes, and waits on
// the health gate. It returns nil once the app is serving. Concurrent callers
// collapse to one start: the loser gets ErrWaking and should serve the waking
// page. A wake on an already-awake app (a prior wake won, or a deploy cleared
// the flag) returns nil immediately.
//
// suspended must be cleared BEFORE Sync: applyApp consults app.Suspended and
// would otherwise re-install wake routes instead of the real ones. On a health
// failure the app is left un-suspended with its (unhealthy) stack started —
// matching the deploy pipeline, which records failure as state rather than
// tearing down. Normal crashloop/health handling then applies.
func (w *Waker) Wake(ctx context.Context, appID string) error {
	if !w.acquire(appID) {
		return ErrWaking
	}
	defer w.release(appID)

	app, err := w.store.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	if !app.Suspended {
		return nil
	}
	project := composeProject(app.Slug)
	if err := w.docker.Start(ctx, project, ""); err != nil {
		return fmt.Errorf("wake %s: start stack: %w", app.Slug, err)
	}
	if err := w.store.SetAppSuspended(ctx, appID, false); err != nil {
		return fmt.Errorf("wake %s: clear suspended: %w", app.Slug, err)
	}
	w.markServices(ctx, appID, serviceStatusRunning)
	// Re-attach edge + push real routes BEFORE WaitHealthy — Caddy can only see
	// upstreams it routes to. Identical order to the deploy pipeline.
	if err := w.proxy.Sync(ctx, appID); err != nil {
		return fmt.Errorf("wake %s: sync routes: %w", app.Slug, err)
	}
	if err := w.proxy.WaitHealthy(ctx, appID); err != nil {
		return fmt.Errorf("wake %s: %w", app.Slug, err)
	}
	// Stamp the wake as activity so a sweeper goroutine queued before this wake
	// (and thus holding a stale "idle" decision) backs off in Suspend's re-check
	// instead of immediately re-suspending the app we just woke.
	if err := w.store.SetLastTrafficAt(ctx, appID, w.now()); err != nil {
		w.logger.Warn("scaletozero: stamp last traffic on wake", "app", app.Slug, "err", err)
	}
	w.logger.Info("scaletozero: woke app", "app", app.Slug)
	return nil
}

// markServices best-effort sets every persisted service of the app to status.
func (w *Waker) markServices(ctx context.Context, appID, status string) {
	services, err := w.store.ListServicesForApp(ctx, appID)
	if err != nil {
		w.logger.Warn("scaletozero: list services", "app", appID, "err", err)
		return
	}
	for _, s := range services {
		if err := w.store.UpdateServiceStatus(ctx, appID, s.ServiceName, status, nil); err != nil {
			w.logger.Warn("scaletozero: update service status", "app", appID, "service", s.ServiceName, "err", err)
		}
	}
}
