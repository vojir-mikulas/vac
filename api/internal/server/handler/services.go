package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type serviceDTO struct {
	ID                 string    `json:"id"`
	AppID              string    `json:"app_id"`
	Name               string    `json:"name"`
	ContainerID        *string   `json:"container_id,omitempty"`
	ExposedPort        *int      `json:"exposed_port,omitempty"`
	InternalPort       *int      `json:"internal_port,omitempty"`
	HealthPath         *string   `json:"health_path,omitempty"`
	Status             string    `json:"status"`
	RestartCount       int       `json:"restart_count"`
	LastExitCode       *int      `json:"last_exit_code,omitempty"`
	HasVolumes         bool      `json:"has_volumes"`
	IsPrivate          bool      `json:"is_private"`
	RequiresAuth       bool      `json:"requires_auth"`
	GuestAccessEnabled bool      `json:"guest_access_enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func toServiceDTO(s store.Service) serviceDTO {
	return serviceDTO{
		ID:                 s.ID,
		AppID:              s.AppID,
		Name:               s.ServiceName,
		ContainerID:        s.ContainerID,
		ExposedPort:        s.ExposedPort,
		InternalPort:       s.InternalPort,
		HealthPath:         s.HealthPath,
		Status:             s.Status,
		RestartCount:       s.RestartCount,
		LastExitCode:       s.LastExitCode,
		HasVolumes:         s.HasVolumes,
		IsPrivate:          s.IsPrivate,
		RequiresAuth:       s.RequiresAuth,
		GuestAccessEnabled: s.GuestAccessEnabled,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
}

// ListAppServices returns the services declared by the app's most recent
// successful deploy.
func ListAppServices(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rows, err := s.ListServicesForApp(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list services")
			return
		}
		out := make([]serviceDTO, 0, len(rows))
		for _, sv := range rows {
			out = append(out, toServiceDTO(sv))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

type patchServiceRequest struct {
	ExposedPort  *int    `json:"exposed_port,omitempty"`
	InternalPort *int    `json:"internal_port,omitempty"`
	HealthPath   *string `json:"health_path,omitempty"`
	IsPrivate    *bool   `json:"is_private,omitempty"`
	RequiresAuth *bool   `json:"requires_auth,omitempty"`
}

// PatchAppService sets the operator-controlled routing fields on a service.
// internal_port is the container port Caddy dials over vac-edge (auto-detected
// from the compose port mapping, but settable when the repo only `expose`s a
// port); health_path is the Caddy active health-check path. Domains are managed
// via the /domains endpoints, not here.
//
// internal_port / health_path feed Caddy routing only — the container's own
// listening port is intrinsic to the image and VAC can't change it. So a change
// to either re-syncs the live Caddy route (new dial port / health-check path)
// rather than redeploying: the effect is immediate, no restart needed. Note the
// next deploy re-detects the port via `docker compose ps` and may overwrite an
// operator-set value (see store.UpsertService) — the override is best-effort,
// not sticky across deploys.
func PatchAppService(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")

		var req patchServiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.ExposedPort != nil && (*req.ExposedPort < 1 || *req.ExposedPort > 65535) {
			WriteError(w, http.StatusBadRequest, "exposed_port must be 1..65535")
			return
		}
		if req.InternalPort != nil && (*req.InternalPort < 1 || *req.InternalPort > 65535) {
			WriteError(w, http.StatusBadRequest, "internal_port must be 1..65535")
			return
		}

		updated, err := s.SetServiceConfig(r.Context(), appID, name, req.ExposedPort, req.InternalPort, req.HealthPath, req.IsPrivate, req.RequiresAuth)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update service")
			return
		}

		// A routing-relevant change must reach Caddy now, not on the next deploy:
		// re-push the app's routes so the upstream dials the new port / health
		// path. proxySync is nil-safe and best-effort (logs on failure).
		if req.InternalPort != nil || req.HealthPath != nil || req.IsPrivate != nil || req.RequiresAuth != nil {
			proxySync(r.Context(), pm, appID)
		}

		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), describeServicePatch(name, req))

		WriteJSON(w, http.StatusOK, toServiceDTO(updated))
	}
}

// describeServicePatch builds the activity-feed summary for a service patch,
// naming only the fields that were actually set.
func describeServicePatch(name string, req patchServiceRequest) string {
	switch {
	case req.InternalPort != nil:
		return fmt.Sprintf("set %s internal port to %d", name, *req.InternalPort)
	case req.HealthPath != nil:
		return fmt.Sprintf("set %s health path to %s", name, *req.HealthPath)
	case req.ExposedPort != nil:
		return fmt.Sprintf("set %s exposed port to %d", name, *req.ExposedPort)
	case req.IsPrivate != nil:
		if *req.IsPrivate {
			return "made " + name + " private (no public route)"
		}
		return "made " + name + " public"
	case req.RequiresAuth != nil:
		if *req.RequiresAuth {
			return "put " + name + " behind VAC login"
		}
		return "removed VAC login gate from " + name
	default:
		return "updated service " + name
	}
}
