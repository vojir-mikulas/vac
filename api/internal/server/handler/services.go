package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type serviceDTO struct {
	ID           string    `json:"id"`
	AppID        string    `json:"app_id"`
	Name         string    `json:"name"`
	ContainerID  *string   `json:"container_id,omitempty"`
	ExposedPort  *int      `json:"exposed_port,omitempty"`
	Domain       *string   `json:"domain,omitempty"`
	Status       string    `json:"status"`
	RestartCount int       `json:"restart_count"`
	LastExitCode *int      `json:"last_exit_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toServiceDTO(s store.Service) serviceDTO {
	return serviceDTO{
		ID:           s.ID,
		AppID:        s.AppID,
		Name:         s.ServiceName,
		ContainerID:  s.ContainerID,
		ExposedPort:  s.ExposedPort,
		Domain:       s.Domain,
		Status:       s.Status,
		RestartCount: s.RestartCount,
		LastExitCode: s.LastExitCode,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
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
	Domain      *string `json:"domain,omitempty"`
	ExposedPort *int    `json:"exposed_port,omitempty"`
}

// PatchAppService sets the operator-controlled fields on a service —
// `domain` (placeholder until Caddy lands in Phase 3) and `exposed_port`
// (override for the health-check fallback in M9).
func PatchAppService(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")

		var req patchServiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Domain != nil {
			trimmed := strings.TrimSpace(*req.Domain)
			// Empty string clears the domain; non-empty must look domain-ish.
			if trimmed != "" && (strings.ContainsAny(trimmed, " \t\n") || !strings.Contains(trimmed, ".")) {
				WriteError(w, http.StatusBadRequest, "domain must be a valid hostname")
				return
			}
			if trimmed == "" {
				req.Domain = nil
			} else {
				req.Domain = &trimmed
			}
		}
		if req.ExposedPort != nil && (*req.ExposedPort < 1 || *req.ExposedPort > 65535) {
			WriteError(w, http.StatusBadRequest, "exposed_port must be 1..65535")
			return
		}

		updated, err := s.SetServiceDomain(r.Context(), appID, name, req.Domain, req.ExposedPort)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update service")
			return
		}
		WriteJSON(w, http.StatusOK, toServiceDTO(updated))
	}
}
