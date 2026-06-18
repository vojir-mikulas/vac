package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/maintenance"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Maintenance mode + editable page (docs/plans/maintenance-mode-and-deploy-gates.md,
// Phase 1+2). The operator can put an app into maintenance so Caddy serves a 503
// page instead of proxying, optionally during deploys, with an optional custom
// page. Toggling re-Syncs the proxy, which reads the flag and swaps every host's
// route to/from the maintenance page in place.

type maintenanceDTO struct {
	Enabled       bool `json:"enabled"`         // operator-set manual maintenance
	Auto          bool `json:"auto"`            // show the page automatically during deploys
	Active        bool `json:"active"`          // effective runtime flag (router reads this)
	HasCustomPage bool `json:"has_custom_page"` // a custom page is stored (vs the default)
}

func toMaintenanceDTO(a store.App) maintenanceDTO {
	return maintenanceDTO{
		Enabled:       a.MaintenanceMode,
		Auto:          a.MaintenanceAuto,
		Active:        a.MaintenanceActive,
		HasCustomPage: a.MaintenanceHTML != nil,
	}
}

// GetMaintenance returns the app's maintenance state.
func GetMaintenance(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		WriteJSON(w, http.StatusOK, toMaintenanceDTO(app))
	}
}

type setMaintenanceRequest struct {
	Enabled bool `json:"enabled"`
	Auto    bool `json:"auto"`
}

// SetMaintenance toggles the manual maintenance flag and the auto-during-deploy
// opt-in, then re-syncs the proxy so the routes swap immediately.
func SetMaintenance(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		var req setMaintenanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.SetAppMaintenance(r.Context(), app.ID, req.Enabled, req.Auto); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not update maintenance")
			return
		}
		// Re-sync so Caddy swaps every host's route to/from the maintenance page.
		proxySync(r.Context(), pm, app.ID)
		audit.SetTarget(r.Context(), "app", app.ID)
		if req.Enabled {
			audit.Describe(r.Context(), "enabled maintenance mode")
		} else {
			audit.Describe(r.Context(), "disabled maintenance mode")
		}
		updated, err := s.GetApp(r.Context(), app.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		WriteJSON(w, http.StatusOK, toMaintenanceDTO(updated))
	}
}

type maintenancePageDTO struct {
	HTML      string `json:"html"`       // the effective page (custom or default)
	IsDefault bool   `json:"is_default"` // true when no custom page is stored
}

// GetMaintenancePage returns the effective maintenance HTML (custom or default)
// so the editor can pre-fill and preview it.
func GetMaintenancePage(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		WriteJSON(w, http.StatusOK, maintenancePageDTO{
			HTML:      maintenance.Render(app.MaintenanceHTML),
			IsDefault: app.MaintenanceHTML == nil,
		})
	}
}

type putMaintenancePageRequest struct {
	HTML string `json:"html"`
}

// PutMaintenancePage stores a custom maintenance page (size-capped), then
// re-syncs so a live maintenance page updates in place.
func PutMaintenancePage(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		var req putMaintenancePageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if verr := maintenance.Validate(req.HTML); verr != nil {
			status := http.StatusBadRequest
			if errors.Is(verr, maintenance.ErrTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			WriteError(w, status, verr.Error())
			return
		}
		html := req.HTML
		if err := s.SetAppMaintenanceHTML(r.Context(), app.ID, &html); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save maintenance page")
			return
		}
		// Push the new page if maintenance is currently active.
		if app.MaintenanceActive {
			proxySync(r.Context(), pm, app.ID)
		}
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Describe(r.Context(), "updated the maintenance page")
		WriteJSON(w, http.StatusOK, maintenancePageDTO{HTML: html, IsDefault: false})
	}
}

// DeleteMaintenancePage reverts to the built-in default page, then re-syncs so a
// live maintenance page reverts in place.
func DeleteMaintenancePage(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		if err := s.SetAppMaintenanceHTML(r.Context(), app.ID, nil); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not reset maintenance page")
			return
		}
		if app.MaintenanceActive {
			proxySync(r.Context(), pm, app.ID)
		}
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Describe(r.Context(), "reset the maintenance page to default")
		WriteJSON(w, http.StatusOK, maintenancePageDTO{
			HTML:      maintenance.DefaultHTML(),
			IsDefault: true,
		})
	}
}
