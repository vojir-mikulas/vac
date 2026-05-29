package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// StatsWS streams per-service stats for an app. Subscribing starts the app's
// stats collector (via the hub's subscribe hook); the last disconnect stops it.
// There is no backlog — stats are live-only — so the handler just subscribes
// and pumps.
//
// GET (WebSocket) /api/apps/:id/stats
func StatsWS(s *store.Store, hub *ws.Hub, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if _, err := s.GetApp(r.Context(), appID); err != nil {
			status, msg := http.StatusInternalServerError, "could not load app"
			if errors.Is(err, store.ErrNotFound) {
				status, msg = http.StatusNotFound, "app not found"
			}
			WriteError(w, status, msg)
			return
		}

		ch, cancel := hub.Subscribe(ws.StatsTopic(appID))
		defer cancel()

		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")

		conn.Pump(r.Context(), ch)
	}
}
