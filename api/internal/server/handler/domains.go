package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// RouteSyncer projects an app's domains into Caddy. The domain handlers call
// it best-effort after a DB change so routing follows the data. May be nil in
// tests / when the proxy is not wired.
type RouteSyncer interface {
	Sync(ctx context.Context, appID string) error
}

type domainDTO struct {
	ID          string    `json:"id"`
	AppID       string    `json:"app_id"`
	ServiceName string    `json:"service_name"`
	Hostname    string    `json:"hostname"`
	Type        string    `json:"type"`
	CertStatus  string    `json:"cert_status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toDomainDTO(d store.Domain) domainDTO {
	return domainDTO{
		ID:          d.ID,
		AppID:       d.AppID,
		ServiceName: d.ServiceName,
		Hostname:    d.Hostname,
		Type:        d.Type,
		CertStatus:  d.CertStatus,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

// ListAppDomains returns every domain across the app's services.
func ListAppDomains(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rows, err := s.ListDomainsByApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list domains")
			return
		}
		out := make([]domainDTO, 0, len(rows))
		for _, d := range rows {
			out = append(out, toDomainDTO(d))
		}
		WriteJSON(w, http.StatusOK, out)
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
