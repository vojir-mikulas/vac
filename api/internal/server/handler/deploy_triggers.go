package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const maxTriggerFilterLen = 200

type deployTriggerDTO struct {
	ID              string    `json:"id"`
	Event           string    `json:"event"`
	Filter          string    `json:"filter"`
	RequireApproval bool      `json:"require_approval"`
	CreatedAt       time.Time `json:"created_at"`
}

func toDeployTriggerDTO(t store.DeployTrigger) deployTriggerDTO {
	return deployTriggerDTO{
		ID:              t.ID,
		Event:           t.Event,
		Filter:          t.Filter,
		RequireApproval: t.RequireApproval,
		CreatedAt:       t.CreatedAt,
	}
}

// ListDeployTriggers returns an app's push-to-deploy rules, oldest first.
func ListDeployTriggers(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rows, err := s.ListDeployTriggers(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list triggers")
			return
		}
		out := make([]deployTriggerDTO, 0, len(rows))
		for _, t := range rows {
			out = append(out, toDeployTriggerDTO(t))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

type createTriggerRequest struct {
	Event           string `json:"event"`
	Filter          string `json:"filter"`
	RequireApproval bool   `json:"require_approval"`
}

// CreateDeployTrigger adds a rule. Only push|tag|preview are accepted — `manual`
// is the absence of an auto-deploy rule, not a webhook event. A `preview` rule
// makes matching non-default branches spin up preview environments
// (preview-deployments.md) instead of redeploying the parent.
func CreateDeployTrigger(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if _, err := s.GetApp(r.Context(), appID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		var req createTriggerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Event != store.TriggerEventPush && req.Event != store.TriggerEventTag && req.Event != store.TriggerEventPreview {
			WriteError(w, http.StatusBadRequest, "event must be 'push', 'tag', or 'preview'")
			return
		}
		if len(req.Filter) > maxTriggerFilterLen {
			WriteError(w, http.StatusBadRequest, "filter too long")
			return
		}
		t, err := s.CreateDeployTrigger(r.Context(), appID, req.Event, req.Filter, req.RequireApproval)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not create trigger")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Action(r.Context(), "deploy_trigger.added", map[string]any{"event": req.Event, "filter": triggerLabel(req.Filter)})
		WriteJSON(w, http.StatusCreated, toDeployTriggerDTO(t))
	}
}

// DeleteDeployTrigger removes a rule by id.
func DeleteDeployTrigger(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		triggerID := chi.URLParam(r, "triggerId")
		if err := s.DeleteDeployTrigger(r.Context(), appID, triggerID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "trigger not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete trigger")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Action(r.Context(), "deploy_trigger.removed", nil)
		WriteJSON(w, http.StatusOK, map[string]int{"deleted": 1})
	}
}

func triggerLabel(filter string) string {
	if filter == "" {
		return "(any ref)"
	}
	return filter
}
