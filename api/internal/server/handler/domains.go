package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/domainstatus"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// RouteSyncer projects an app's domains into Caddy. The domain handlers call
// it best-effort after a DB change so routing follows the data. May be nil in
// tests / when the proxy is not wired.
type RouteSyncer interface {
	Sync(ctx context.Context, appID string) error
}

// DomainStatusProvider reads the live DNS/cert status projection (plan 09 F3).
// The proxy-wired *domainstatus.Engine satisfies it; nil omits status fields.
type DomainStatusProvider interface {
	Get(host string) (domainstatus.Status, bool)
	Refresh(ctx context.Context, host string) (domainstatus.Status, bool)
}

// AutoHostLister enumerates the derived automatic subdomains for the Domains
// hub. *proxy.Manager satisfies it; nil yields no managed rows.
type AutoHostLister interface {
	AutoHosts(ctx context.Context) ([]proxy.AutoHost, error)
}

// withStatus folds the live status projection into a domain DTO. A nil provider
// (or an unobserved host) leaves the status fields at their zero/checking value.
func withStatus(dto domainDTO, status DomainStatusProvider) domainDTO {
	if status == nil {
		return dto
	}
	if st, ok := status.Get(dto.Hostname); ok {
		dto.Status = st.State
		dto.StatusDetail = st.Detail
		dto.CertNotAfter = st.CertNotAfter
		dto.LastChecked = st.LastChecked
	} else {
		dto.Status = domainstatus.StateChecking
	}
	return dto
}

type domainDTO struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	ServiceName string `json:"service_name"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Managed     bool   `json:"managed"`               // derived auto host (read-only); custom rows are false
	RedirectTo  string `json:"redirect_to,omitempty"` // Phase 3: 308 redirect target
	// Live DNS/cert status projection (plan 09 F3). Zero values until the status
	// engine is wired / has observed the host.
	Status       string     `json:"status,omitempty"`
	StatusDetail string     `json:"status_detail,omitempty"`
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	LastChecked  *time.Time `json:"last_checked,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func toDomainDTO(d store.Domain) domainDTO {
	return domainDTO{
		ID:          d.ID,
		AppID:       d.AppID,
		ServiceName: d.ServiceName,
		Hostname:    d.Hostname,
		Type:        d.Type,
		RedirectTo:  d.RedirectTo,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

// ListAppDomains returns every custom domain bound to the app, plus its derived
// auto hosts (read-only/managed), each carrying live DNS/cert status.
func ListAppDomains(s *store.Store, status DomainStatusProvider, autos AutoHostLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rows, err := s.ListDomainsByApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list domains")
			return
		}
		out := make([]domainDTO, 0, len(rows))
		for _, d := range rows {
			out = append(out, withStatus(toDomainDTO(d), status))
		}
		// Append this app's derived auto hosts (no row backs them).
		if autos != nil {
			if hosts, err := autos.AutoHosts(r.Context()); err == nil {
				for _, h := range hosts {
					if h.AppID != appID {
						continue
					}
					out = append(out, withStatus(autoHostDTO(h), status))
				}
			}
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// autoHostDTO renders a derived auto host as a managed (read-only) domain row.
// It has no id (no backing row); the UI refreshes it by hostname.
func autoHostDTO(h proxy.AutoHost) domainDTO {
	return domainDTO{
		AppID:       h.AppID,
		ServiceName: h.ServiceName,
		Hostname:    h.Hostname,
		Type:        store.DomainTypeAuto,
		Managed:     true,
	}
}

type addDomainRequest struct {
	Hostname string `json:"hostname"`
}

// AddCustomDomain attaches a custom hostname to a service and syncs routing.
func AddCustomDomain(s *store.Store, syncer RouteSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")

		var req addDomainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		host, err := proxy.NormalizeHostname(req.Hostname)
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}

		// The service must exist before we can attach a domain to it (the
		// composite FK would reject it anyway, but a 404 is clearer than 500).
		if _, err := s.GetService(r.Context(), appID, name); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load service")
			return
		}

		d, err := s.CreateDomain(r.Context(), appID, name, host, store.DomainTypeCustom)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "hostname already in use")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create domain")
			return
		}

		syncRoutes(r.Context(), syncer, appID)
		WriteJSON(w, http.StatusCreated, toDomainDTO(d))
	}
}

// DeleteAppDomain removes a domain and syncs routing.
func DeleteAppDomain(s *store.Store, syncer RouteSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		domainID := chi.URLParam(r, "domainId")

		if err := s.DeleteDomain(r.Context(), appID, domainID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete domain")
			return
		}

		syncRoutes(r.Context(), syncer, appID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---- Domains hub (plan 09 Phase 1 & 2): manage every domain in one place ----

// ListDomainsHub returns every custom domain across all apps plus every derived
// auto host, each with live status — the Settings → Domains hub.
func ListDomainsHub(s *store.Store, status DomainStatusProvider, autos AutoHostLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListAllDomains(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list domains")
			return
		}
		out := make([]domainDTO, 0, len(rows))
		for _, d := range rows {
			out = append(out, withStatus(toDomainDTO(d), status))
		}
		if autos != nil {
			if hosts, err := autos.AutoHosts(r.Context()); err == nil {
				for _, h := range hosts {
					out = append(out, withStatus(autoHostDTO(h), status))
				}
			}
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

type hubDomainRequest struct {
	Hostname    string `json:"hostname"`
	AppID       string `json:"app_id"`
	ServiceName string `json:"service_name"`
	RedirectTo  string `json:"redirect_to"`
}

// validateAssignment checks the both-or-neither rule and, when assigned, that
// the service exists. Returns the (possibly empty) app/service to persist.
func validateAssignment(ctx context.Context, s *store.Store, appID, serviceName string) (string, string, error) {
	if (appID == "") != (serviceName == "") {
		return "", "", errors.New("assign both an app and a service, or leave both blank")
	}
	if appID != "" {
		if _, err := s.GetService(ctx, appID, serviceName); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "", "", errors.New("service not found")
			}
			return "", "", errors.New("could not load service")
		}
	}
	return appID, serviceName, nil
}

// AddDomainHub adds a custom domain, optionally assigned to an app/service. An
// unassigned domain is DNS-verifiable immediately but emits no route until it is
// bound (plan 09 Phase 1).
func AddDomainHub(s *store.Store, syncer RouteSyncer, status DomainStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req hubDomainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		host, err := proxy.NormalizeHostname(req.Hostname)
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		appID, service, err := validateAssignment(r.Context(), s, req.AppID, req.ServiceName)
		if err != nil {
			code := http.StatusBadRequest
			if err.Error() == "service not found" {
				code = http.StatusNotFound
			}
			WriteError(w, code, err.Error())
			return
		}
		d, err := s.CreateDomain(r.Context(), appID, service, host, store.DomainTypeCustom)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "hostname already in use")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create domain")
			return
		}
		if d.Assigned() {
			syncRoutes(r.Context(), syncer, d.AppID)
		}
		WriteJSON(w, http.StatusCreated, withStatus(toDomainDTO(d), status))
	}
}

// UpdateDomainHub re-points a custom domain to another service or app, and/or
// renames its hostname — an in-place route swap (plan 09 Phase 2), never a
// destructive delete-add. Auto hosts have no row and can't be edited here.
func UpdateDomainHub(s *store.Store, syncer RouteSyncer, status DomainStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := s.GetDomainByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load domain")
			return
		}

		var req hubDomainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		host := existing.Hostname
		if req.Hostname != "" {
			host, err = proxy.NormalizeHostname(req.Hostname)
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		appID, service, err := validateAssignment(r.Context(), s, req.AppID, req.ServiceName)
		if err != nil {
			code := http.StatusBadRequest
			if err.Error() == "service not found" {
				code = http.StatusNotFound
			}
			WriteError(w, code, err.Error())
			return
		}

		// A redirect (Phase 3) must be assigned (it routes under an app) and point
		// at a different, valid hostname.
		var redirectTo string
		if req.RedirectTo != "" {
			redirectTo, err = proxy.NormalizeHostname(req.RedirectTo)
			if err != nil {
				WriteError(w, http.StatusBadRequest, "invalid redirect target: "+err.Error())
				return
			}
			if appID == "" {
				WriteError(w, http.StatusBadRequest, "assign the domain to an app before adding a redirect")
				return
			}
			if redirectTo == host {
				WriteError(w, http.StatusBadRequest, "a domain cannot redirect to itself")
				return
			}
		}

		updated, err := s.UpdateDomain(r.Context(), id, appID, service, host, redirectTo)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "hostname already in use")
				return
			}
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update domain")
			return
		}
		// Re-sync both the old and new owning apps so a moved route lands in one
		// place and is pruned from the other.
		if existing.Assigned() {
			syncRoutes(r.Context(), syncer, existing.AppID)
		}
		if updated.Assigned() && updated.AppID != existing.AppID {
			syncRoutes(r.Context(), syncer, updated.AppID)
		}
		WriteJSON(w, http.StatusOK, withStatus(toDomainDTO(updated), status))
	}
}

// DeleteDomainHub deletes a custom domain by id (assigned or not) and prunes its
// route. Auto hosts aren't deletable (they have no row).
func DeleteDomainHub(s *store.Store, syncer RouteSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := s.GetDomainByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load domain")
			return
		}
		if err := s.DeleteDomainByID(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete domain")
			return
		}
		if existing.Assigned() {
			syncRoutes(r.Context(), syncer, existing.AppID)
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// RefreshDomainStatus forces an immediate re-probe of one host's status (the
// "Refresh" affordance), bypassing the poll cadence but honouring the engine's
// short per-host cache. Works for both custom domains and auto hosts (by host).
func RefreshDomainStatus(status DomainStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, err := proxy.NormalizeHostname(r.URL.Query().Get("host"))
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if status == nil {
			WriteJSON(w, http.StatusOK, domainstatus.Status{State: domainstatus.StateChecking})
			return
		}
		st, ok := status.Refresh(r.Context(), host)
		if !ok {
			WriteError(w, http.StatusNotFound, "unknown host")
			return
		}
		WriteJSON(w, http.StatusOK, st)
	}
}

// syncRoutes runs a best-effort Caddy sync. A failure here never fails the
// request — the DB is the source of truth and the boot reconcile (or the next
// deploy) will converge Caddy.
func syncRoutes(ctx context.Context, syncer RouteSyncer, appID string) {
	if syncer == nil {
		return
	}
	if err := syncer.Sync(ctx, appID); err != nil {
		slog.Warn("proxy sync after domain change failed", "app", appID, "err", err)
	}
}
