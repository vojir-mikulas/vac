package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// effectiveLabel renders a base-domain value for an audit summary, naming the
// empty (cleared) case rather than logging a blank.
func effectiveLabel(host string) string {
	if host == "" {
		return "(cleared)"
	}
	return host
}

// ControlPlaneRestarter bounces raw infrastructure containers by name.
// *dockercli.Compose satisfies it.
type ControlPlaneRestarter interface {
	RestartContainers(ctx context.Context, names ...string) error
}

// AppStackController stops and removes per-app compose stacks. *dockercli.Compose
// satisfies it. Used by the instance-level stop-all / reset operations.
type AppStackController interface {
	Stop(ctx context.Context, projectName, service string) error
	Down(ctx context.Context, projectName string, removeVolumes bool) error
}

// BaseDomainSetter applies a runtime base-domain override to the live proxy.
// *proxy.Manager satisfies it. May be nil when the proxy isn't wired.
type BaseDomainSetter interface {
	SetBaseDomain(domain string)
}

// selfTerminate asks the current process to begin graceful shutdown so the
// container restart policy (`restart: unless-stopped`) brings vac-api back.
// Overridable in tests. See docs/deviations.md for the restart mechanism.
var selfTerminate = func() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}

// InstanceInfo reports build/version metadata for the Instance settings tab.
//
// GET /api/instance/info
func InstanceInfo(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{
			"version":  cfg.Version,
			"commit":   cfg.Commit,
			"built_at": cfg.BuildDate,
			// Update channels / auto-update are not implemented; the UI renders
			// these as disabled placeholders. Reported for display only.
			"channel": "stable",
			// Track D master gate. The UI hides managed-services surfaces (backups,
			// databases, add-ons) until this is on.
			"managed_services": cfg.ManagedServices,
		})
	}
}

// GetBaseDomain returns the runtime base-domain override plus the effective
// value (override or config fallback) for the Domains settings tab.
//
// GET /api/instance/base-domain
func GetBaseDomain(s *store.Store, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.GetInstanceSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load instance settings")
			return
		}
		effective := settings.BaseDomain
		if effective == "" {
			effective = cfg.BaseDomain
		}
		WriteJSON(w, http.StatusOK, map[string]string{
			"base_domain": settings.BaseDomain,
			"effective":   effective,
		})
	}
}

type baseDomainRequest struct {
	BaseDomain string `json:"base_domain"`
}

// PutBaseDomain validates and persists the instance base domain, then applies it
// to the live proxy so new auto-subdomains use it immediately. An empty value
// clears the override (falling back to config).
//
// PUT /api/instance/base-domain
func PutBaseDomain(s *store.Store, cfg config.Config, pm BaseDomainSetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req baseDomainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		host := strings.TrimSpace(req.BaseDomain)
		if host != "" {
			normalized, err := proxy.NormalizeHostname(host)
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			host = normalized
		}
		// Curated-revert snapshot: capture the prior override so the change can be
		// undone. Best-effort — a read failure must not block the save.
		if prior, err := s.GetInstanceSettings(r.Context()); err == nil {
			audit.Snapshot(r.Context(), map[string]any{"base_domain": prior.BaseDomain})
		}
		audit.Describe(r.Context(), "set base domain to "+effectiveLabel(host))
		if err := s.SetBaseDomain(r.Context(), host); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save base domain")
			return
		}
		if pm != nil {
			pm.SetBaseDomain(host)
		}
		effective := host
		if effective == "" {
			effective = cfg.BaseDomain
		}
		WriteJSON(w, http.StatusOK, map[string]string{
			"base_domain": host,
			"effective":   effective,
		})
	}
}

// onboardingDTO is the first-run checklist state (plan 04). `dismissed` is the
// only persisted bit; the per-step completion is derived client-side from the
// apps list and base-domain it already loads.
type onboardingDTO struct {
	Dismissed bool `json:"dismissed"`
}

// GetOnboarding reports whether the operator has dismissed the first-run
// checklist.
//
// GET /api/instance/onboarding
func GetOnboarding(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.GetInstanceSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load onboarding state")
			return
		}
		WriteJSON(w, http.StatusOK, onboardingDTO{Dismissed: settings.OnboardingDismissed})
	}
}

// DismissOnboarding permanently dismisses the first-run checklist.
//
// POST /api/instance/onboarding/dismiss
func DismissOnboarding(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.SetOnboardingDismissed(r.Context(), true); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not dismiss onboarding")
			return
		}
		audit.Skip(r.Context()) // routine UI state toggle — not worth an audit row
		WriteJSON(w, http.StatusOK, onboardingDTO{Dismissed: true})
	}
}

// DNSCheck resolves a hostname server-side and reports whether it currently
// points at this VPS — the in-app answer to "is my domain pointed here yet?".
//
// GET /api/instance/dns-check?host=app.example.com
func DNSCheck(hostIP string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, err := proxy.NormalizeHostname(r.URL.Query().Get("host"))
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		addrs, lookupErr := net.DefaultResolver.LookupHost(ctx, host)

		resolved := make([]string, 0, len(addrs))
		pointsHere := false
		for _, a := range addrs {
			resolved = append(resolved, a)
			if hostIP != "" && a == hostIP {
				pointsHere = true
			}
		}
		resp := map[string]any{
			"host":        host,
			"ip":          hostIP,
			"resolved":    resolved,
			"points_here": pointsHere,
		}
		if lookupErr != nil {
			// NXDOMAIN / no records is a normal "not pointed yet" state, not a
			// server error — report it as an unresolved result.
			resp["error"] = "could not resolve hostname"
		}
		WriteJSON(w, http.StatusOK, resp)
	}
}

// RestartControlPlane bounces the vac-proxy container, then asks vac-api to
// gracefully exit so its restart policy brings it (and the in-process worker)
// back. App containers on vac-edge are untouched. See docs/deviations.md.
//
// POST /api/instance/restart-control-plane
func RestartControlPlane(ctrl ControlPlaneRestarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Restart the proxy synchronously (it comes back on its own and
		// vac-api's self-heal re-pushes the base config).
		if err := ctrl.RestartContainers(r.Context(), "vac-proxy"); err != nil {
			slog.Warn("instance: restart vac-proxy failed", "err", err)
		}
		// Respond before we bounce ourselves; the client shows a reconnecting
		// state until the API answers again.
		WriteJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
		go func() {
			time.Sleep(500 * time.Millisecond)
			slog.Info("instance: restarting control plane (self-exit)")
			selfTerminate()
		}()
	}
}

// StopAllApps stops every VAC-managed app stack. The control plane keeps
// running. Idempotent — already-stopped apps are left alone.
//
// POST /api/instance/stop-all-apps
func StopAllApps(s *store.Store, ctrl AppStackController, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apps, err := s.ListApps(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list apps")
			return
		}
		var stopped, failed int
		for _, app := range apps {
			project := "vac-" + app.Slug
			if err := ctrl.Stop(r.Context(), project, ""); err != nil {
				slog.Warn("instance: stop-all could not stop stack", "app", app.ID, "err", err)
				failed++
				continue
			}
			applyStatusToAll(r.Context(), s, app.ID, deploy.ServiceStatusStopped)
			_ = s.SetAppStatus(r.Context(), app.ID, deploy.AppStatusStopped)
			proxyTeardown(r.Context(), pm, app.ID)
			stopped++
		}
		WriteJSON(w, http.StatusOK, map[string]int{"stopped": stopped, "failed": failed})
	}
}

type resetRequest struct {
	Confirm string `json:"confirm"`
}

// resetConfirmation is the exact phrase the operator must type to authorize a
// reset; re-validated server-side so a client bug can't trigger it.
const resetConfirmation = "RESET"

// ResetInstance is the irreversible nuke: it tears down and removes every app
// stack (including volumes) and deletes all app rows (cascading deployments,
// services, domains, env). The control plane and its DB schema survive. Guarded
// by a typed confirmation echoed in the body.
//
// POST /api/instance/reset
func ResetInstance(s *store.Store, ctrl AppStackController, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req resetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(req.Confirm) != resetConfirmation {
			WriteError(w, http.StatusBadRequest, "confirmation phrase does not match")
			return
		}

		apps, err := s.ListApps(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list apps")
			return
		}
		slog.Warn("instance: RESET requested — wiping all apps", "count", len(apps))

		var removed, failed int
		for _, app := range apps {
			project := "vac-" + app.Slug
			// Remove containers + volumes; best-effort so a stuck stack can't
			// block the wipe.
			if err := ctrl.Down(r.Context(), project, true); err != nil {
				slog.Warn("instance: reset could not down stack", "app", app.ID, "err", err)
				failed++
			}
			proxyTeardown(r.Context(), pm, app.ID)
			if err := s.DeleteApp(r.Context(), app.ID); err != nil {
				slog.Warn("instance: reset could not delete app row", "app", app.ID, "err", err)
				failed++
				continue
			}
			removed++
		}
		slog.Warn("instance: RESET complete", "removed", removed, "failed", failed)
		WriteJSON(w, http.StatusOK, map[string]int{"removed": removed, "failed": failed})
	}
}
