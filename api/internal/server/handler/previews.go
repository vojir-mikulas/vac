package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/preview"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// previewDTO is the wire shape for a preview environment in the parent app's
// Previews tab. URL is the primary derived auto-host (https), Hosts lists all of
// them (multi-service previews expose more than one).
type previewDTO struct {
	ID         string     `json:"id"`
	Slug       string     `json:"slug"`
	Branch     string     `json:"branch"`
	Status     string     `json:"status"`
	URL        string     `json:"url,omitempty"`
	Hosts      []string   `json:"hosts"`
	CreatedAt  time.Time  `json:"created_at"`
	LastPushAt *time.Time `json:"last_push_at,omitempty"`
}

// ListPreviews returns a parent app's preview environments with their derived
// URLs resolved from the live auto-host set (so the URL is empty until the
// preview has a routable HTTP service, exactly like the Domains tab).
func ListPreviews(s *store.Store, autos AutoHostLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rows, err := s.ListPreviewsForApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list previews")
			return
		}

		// Resolve derived hosts once, grouped by app id.
		hostsByApp := map[string][]string{}
		if autos != nil {
			if hosts, err := autos.AutoHosts(r.Context()); err == nil {
				for _, h := range hosts {
					hostsByApp[h.AppID] = append(hostsByApp[h.AppID], h.Hostname)
				}
			}
		}

		out := make([]previewDTO, 0, len(rows))
		for _, a := range rows {
			d := previewDTO{
				ID:         a.ID,
				Slug:       a.Slug,
				Branch:     a.GitBranch,
				Status:     a.Status,
				Hosts:      hostsByApp[a.ID],
				CreatedAt:  a.CreatedAt,
				LastPushAt: a.LastPreviewPushAt,
			}
			if len(d.Hosts) > 0 {
				d.URL = "https://" + d.Hosts[0]
			}
			out = append(out, d)
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// TeardownPreview reaps a single preview (compose down -v + delete + reconcile).
// It validates that the preview belongs to the app in the path so one app can't
// tear down another's preview, and refuses on a non-preview id. Step-up gated by
// the router (it's destructive — removes volumes).
func TeardownPreview(s *store.Store, previews PreviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if previews == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "preview deployments are not enabled")
			return
		}
		appID := chi.URLParam(r, "id")
		previewID := chi.URLParam(r, "previewId")

		pv, err := s.GetApp(r.Context(), previewID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "preview not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load preview")
			return
		}
		if !pv.IsPreview || pv.ParentAppID == nil || *pv.ParentAppID != appID {
			WriteError(w, http.StatusNotFound, "preview not found")
			return
		}

		if err := previews.Teardown(r.Context(), previewID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "preview not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not tear down preview")
			return
		}
		audit.SetTarget(r.Context(), "app", previewID)
		audit.Action(r.Context(), "preview.torn_down", map[string]any{"slug": pv.Slug})
		WriteJSON(w, http.StatusOK, map[string]int{"deleted": 1})
	}
}

// previewBudgetDTO reports the global preview count against the cap so the UI can
// show "3 / 5 previews" and disable preview creation guidance when full.
type previewBudgetDTO struct {
	Used int `json:"used"`
	Max  int `json:"max"`
}

// PreviewBudget reports the instance-wide preview count vs VAC_MAX_PREVIEWS.
func PreviewBudget(s *store.Store, previews PreviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		used, err := s.CountPreviews(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not count previews")
			return
		}
		max := 0
		if previews != nil {
			max = previews.MaxPreviews()
		}
		WriteJSON(w, http.StatusOK, previewBudgetDTO{Used: used, Max: max})
	}
}

// compile-time guard: *preview.Service satisfies PreviewService.
var _ PreviewService = (*preview.Service)(nil)
