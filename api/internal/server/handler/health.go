package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// Health distinguishes "binary up" from "DB up" by pinging the pool. Returns
// 503 when the DB is unreachable so load balancers can take this instance
// out of rotation even while the process itself is alive.
func Health(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			// No store wired in (some unit tests) — report binary OK.
			WriteJSON(w, http.StatusOK, healthResponse{Status: "ok", Database: "skipped"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Ping(ctx); err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "degraded", Database: "down"})
			return
		}
		WriteJSON(w, http.StatusOK, healthResponse{Status: "ok", Database: "ok"})
	}
}
