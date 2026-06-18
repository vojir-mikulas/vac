package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Scale-to-zero per-app config (docs/plans/scale-to-zero.md). The operator opts
// an app into idle-suspend and optionally overrides the inactivity window; the
// sweeper (gated by the instance VAC_IDLE_SUSPEND flag) then stops it when idle.
// Toggling here only writes config — it never suspends or wakes directly.

// maxIdleTimeoutMinutes caps the per-app override at 30 days so a typo can't
// effectively disable suspension forever.
const maxIdleTimeoutMinutes = 30 * 24 * 60

type idleSuspendDTO struct {
	Enabled        bool       `json:"enabled"`
	TimeoutMinutes *int       `json:"timeout_minutes"`
	Suspended      bool       `json:"suspended"`
	LastTrafficAt  *time.Time `json:"last_traffic_at,omitempty"`
}

func toIdleSuspendDTO(a store.App) idleSuspendDTO {
	return idleSuspendDTO{
		Enabled:        a.IdleSuspendEnabled,
		TimeoutMinutes: a.IdleTimeoutMinutes,
		Suspended:      a.Suspended,
		LastTrafficAt:  a.LastTrafficAt,
	}
}

// GetIdleSuspend returns the app's scale-to-zero config + runtime state.
func GetIdleSuspend(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		WriteJSON(w, http.StatusOK, toIdleSuspendDTO(app))
	}
}

type setIdleSuspendRequest struct {
	Enabled        bool `json:"enabled"`
	TimeoutMinutes *int `json:"timeout_minutes"`
}

// SetIdleSuspend updates the per-app opt-in and optional timeout override. A
// non-positive timeout clears the override (the instance default applies).
func SetIdleSuspend(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		var req setIdleSuspendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		var timeout *int
		if req.TimeoutMinutes != nil && *req.TimeoutMinutes > 0 {
			v := *req.TimeoutMinutes
			if v > maxIdleTimeoutMinutes {
				v = maxIdleTimeoutMinutes
			}
			timeout = &v
		}
		if err := s.SetIdleSuspendConfig(r.Context(), app.ID, req.Enabled, timeout); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not update idle-suspend")
			return
		}
		audit.SetTarget(r.Context(), "app", app.ID)
		if req.Enabled {
			audit.Describe(r.Context(), "enabled idle-suspend")
		} else {
			audit.Describe(r.Context(), "disabled idle-suspend")
		}
		updated, err := s.GetApp(r.Context(), app.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		WriteJSON(w, http.StatusOK, toIdleSuspendDTO(updated))
	}
}
