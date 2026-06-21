package handler

import (
	"encoding/json"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/webhook"
)

// Deploy windows (maintenance-mode-and-deploy-gates.md, Phase 3): restrict
// push-to-deploy to one or more time windows. A push outside every window is
// parked as a `scheduled` deploy and released by the sweeper when a window opens.

type deployWindowDTO struct {
	// Windows is the schedule; an empty list means "always allowed" (the default).
	Windows []webhook.Window `json:"windows"`
}

// GetDeployWindow returns the app's deploy-window schedule.
func GetDeployWindow(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		windows, perr := webhook.ParseWindows(app.DeployWindow)
		if perr != nil {
			// Corrupt stored value — surface an empty schedule rather than 500 so the
			// operator can overwrite it.
			windows = nil
		}
		if windows == nil {
			windows = []webhook.Window{}
		}
		WriteJSON(w, http.StatusOK, deployWindowDTO{Windows: windows})
	}
}

// PutDeployWindow replaces the app's deploy-window schedule. An empty list
// clears it (always allowed).
func PutDeployWindow(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		var req deployWindowDTO
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if verr := webhook.ValidateWindows(req.Windows); verr != nil {
			WriteError(w, http.StatusBadRequest, verr.Error())
			return
		}
		var raw json.RawMessage
		if len(req.Windows) > 0 {
			b, merr := json.Marshal(req.Windows)
			if merr != nil {
				WriteError(w, http.StatusInternalServerError, "could not encode windows")
				return
			}
			raw = b
		}
		if err := s.SetAppDeployWindow(r.Context(), app.ID, raw); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save deploy window")
			return
		}
		audit.SetTarget(r.Context(), "app", app.ID)
		if len(req.Windows) == 0 {
			audit.Action(r.Context(), "deploy_window.cleared", nil)
		} else {
			audit.Action(r.Context(), "deploy_window.updated", nil)
		}
		out := req.Windows
		if out == nil {
			out = []webhook.Window{}
		}
		WriteJSON(w, http.StatusOK, deployWindowDTO{Windows: out})
	}
}
