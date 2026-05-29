package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type deploymentLogDTO struct {
	ID          int64     `json:"id"`
	ServiceName *string   `json:"service_name,omitempty"`
	Stream      string    `json:"stream"`
	Message     string    `json:"message"`
	Timestamp   time.Time `json:"ts"`
}

// GetDeploymentLogs returns paginated build logs for a deployment. Use
// `?after=<id>&limit=N` to step through the stream — the response uses the
// same ascending id order the writer produced.
func GetDeploymentLogs(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did := chi.URLParam(r, "did")
		afterID, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, err := s.ListDeploymentLogs(r.Context(), did, afterID, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list logs")
			return
		}
		out := make([]deploymentLogDTO, 0, len(rows))
		for _, r := range rows {
			out = append(out, deploymentLogDTO{
				ID:          r.ID,
				ServiceName: r.ServiceName,
				Stream:      r.Stream,
				Message:     r.Message,
				Timestamp:   r.Timestamp,
			})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}
