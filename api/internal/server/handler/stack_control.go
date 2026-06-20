package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// StackController abstracts the docker compose commands so handler tests
// can substitute a fake.
type StackController interface {
	Start(ctx context.Context, projectName, service string) error
	Stop(ctx context.Context, projectName, service string) error
	Restart(ctx context.Context, projectName, service string) error
}

// ProxyManager is the slice of *proxy.Manager the lifecycle handlers use to
// keep Caddy routing + vac-edge attachments in step with the stack. Nil-safe.
type ProxyManager interface {
	Sync(ctx context.Context, appID string) error
	Teardown(ctx context.Context, appID string) error
}

// CrashLoopResetter re-arms crash-loop monitoring for a service (or whole
// project) the operator is intentionally recovering. Without it, a tripped
// service's in-memory flag persists and the monitor stays blind to it until a
// redeploy. *crashloop.Monitor satisfies it; nil-safe via the helpers below.
type CrashLoopResetter interface {
	Reset(projectName, service string)
	ResetProject(projectName string)
}

func crashResetService(cr CrashLoopResetter, project, service string) {
	if cr == nil {
		return
	}
	cr.Reset(project, service)
}

func crashResetProject(cr CrashLoopResetter, project string) {
	if cr == nil {
		return
	}
	cr.ResetProject(project)
}

func proxySync(ctx context.Context, pm ProxyManager, appID string) {
	if pm == nil {
		return
	}
	if err := pm.Sync(ctx, appID); err != nil {
		slog.Warn("proxy sync failed", "app", appID, "err", err)
	}
}

func proxyTeardown(ctx context.Context, pm ProxyManager, appID string) {
	if pm == nil {
		return
	}
	if err := pm.Teardown(ctx, appID); err != nil {
		slog.Warn("proxy teardown failed", "app", appID, "err", err)
	}
}

// StartApp starts all stopped services for the app's compose project.
func StartApp(s *store.Store, ctrl StackController, pm ProxyManager, cr CrashLoopResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		project := "vac-" + app.Slug
		if err := ctrl.Start(r.Context(), project, ""); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not start stack: "+err.Error())
			return
		}
		// Re-arm crash-loop monitoring: this is an explicit operator recovery.
		crashResetProject(cr, project)
		applyStatusToAll(r.Context(), s, app.ID, deploy.ServiceStatusRunning)
		_ = s.SetAppStatus(r.Context(), app.ID, deploy.AppStatusRunning)
		proxySync(r.Context(), pm, app.ID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
	}
}

// StopApp stops all services in the stack and pulls its live routes so a
// stopped app returns a clean 502/503 rather than proxying to a dead upstream.
func StopApp(s *store.Store, ctrl StackController, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		project := "vac-" + app.Slug
		if err := ctrl.Stop(r.Context(), project, ""); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not stop stack: "+err.Error())
			return
		}
		applyStatusToAll(r.Context(), s, app.ID, deploy.ServiceStatusStopped)
		_ = s.SetAppStatus(r.Context(), app.ID, deploy.AppStatusStopped)
		proxyTeardown(r.Context(), pm, app.ID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// RestartApp restarts every service in the stack.
func RestartApp(s *store.Store, ctrl StackController, pm ProxyManager, cr CrashLoopResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		project := "vac-" + app.Slug
		if err := ctrl.Restart(r.Context(), project, ""); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not restart stack: "+err.Error())
			return
		}
		// Re-arm crash-loop monitoring: this is an explicit operator recovery.
		crashResetProject(cr, project)
		proxySync(r.Context(), pm, app.ID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	}
}

// RestartService restarts a single named service.
func RestartService(s *store.Store, ctrl StackController, pm ProxyManager, cr CrashLoopResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		name := chi.URLParam(r, "name")
		project := "vac-" + app.Slug
		if err := ctrl.Restart(r.Context(), project, name); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not restart service: "+err.Error())
			return
		}
		// Clear the crash-loop marker (DB) and re-arm in-memory monitoring: the
		// operator is intentionally recovering this service.
		_ = s.UpdateServiceStatus(r.Context(), app.ID, name, deploy.ServiceStatusRunning, nil)
		crashResetService(cr, project, name)
		proxySync(r.Context(), pm, app.ID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	}
}

// StopService stops a single named service and pulls it from Caddy's upstreams
// so a stopped container doesn't linger as a dead route. The rest of the stack
// keeps serving.
func StopService(s *store.Store, ctrl StackController, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		name := chi.URLParam(r, "name")
		project := "vac-" + app.Slug
		if err := ctrl.Stop(r.Context(), project, name); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not stop service: "+err.Error())
			return
		}
		_ = s.UpdateServiceStatus(r.Context(), app.ID, name, deploy.ServiceStatusStopped, nil)
		// Re-sync rather than teardown: only this service should leave the
		// upstreams; the rest of the app's routes stay up.
		proxySync(r.Context(), pm, app.ID)
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Describe(r.Context(), "stopped service "+name)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// StartService starts a single previously-stopped service without cycling the
// whole stack, then re-syncs Caddy so its upstream comes back.
func StartService(s *store.Store, ctrl StackController, pm ProxyManager, cr CrashLoopResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		name := chi.URLParam(r, "name")
		project := "vac-" + app.Slug
		if err := ctrl.Start(r.Context(), project, name); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not start service: "+err.Error())
			return
		}
		// Re-arm crash-loop monitoring: explicit operator recovery.
		crashResetService(cr, project, name)
		_ = s.UpdateServiceStatus(r.Context(), app.ID, name, deploy.ServiceStatusRunning, nil)
		proxySync(r.Context(), pm, app.ID)
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Describe(r.Context(), "started service "+name)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
	}
}

// loadApp is the boilerplate "GetApp + write 404 / 500" for the lifecycle
// handlers. Writes the error response directly when not nil.
func loadApp(w http.ResponseWriter, r *http.Request, s *store.Store) (store.App, error) {
	id := chi.URLParam(r, "id")
	app, err := s.GetApp(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "app not found")
			return store.App{}, err
		}
		WriteError(w, http.StatusInternalServerError, "could not load app")
		return store.App{}, err
	}
	return app, nil
}

// applyStatusToAll updates every persisted service for the app to the same
// status — used after a whole-stack start/stop to keep the rows in sync
// without waiting for the next deploy.
func applyStatusToAll(ctx context.Context, s *store.Store, appID, status string) {
	rows, err := s.ListServicesForApp(ctx, appID)
	if err != nil {
		return
	}
	for _, r := range rows {
		_ = s.UpdateServiceStatus(ctx, appID, r.ServiceName, status, nil)
	}
}
