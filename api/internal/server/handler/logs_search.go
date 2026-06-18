package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// RuntimeLogSearcher reads runtime logs with the Log Explorer filter set.
// *store.Store satisfies it.
type RuntimeLogSearcher interface {
	SearchRuntimeLogs(ctx context.Context, q store.RuntimeLogQuery) ([]store.RuntimeLog, error)
}

// searchLogDTO is one row in the explorer result. It carries app_id + service
// because the explorer searches across all apps.
type searchLogDTO struct {
	ID      int64     `json:"id"`
	AppID   string    `json:"app_id"`
	Service string    `json:"service"`
	Stream  string    `json:"stream"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// searchLogsResponse pages with a descending-id cursor. NextBefore is 0 when
// the page is the last one (fewer rows than the requested limit).
type searchLogsResponse struct {
	Logs       []searchLogDTO `json:"logs"`
	NextBefore int64          `json:"next_before"`
}

// SearchRuntimeLogsHandler serves GET /api/logs/search — free-text search over
// the runtime-log ring buffer across apps. Query params: app, service, stream,
// q (substring), before (cursor), limit. Results are newest-first.
func SearchRuntimeLogsHandler(s RuntimeLogSearcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		qp := r.URL.Query()
		limit := 200
		if v := qp.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		var before int64
		if v := qp.Get("before"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				before = n
			}
		}
		rows, err := s.SearchRuntimeLogs(r.Context(), store.RuntimeLogQuery{
			AppID:       qp.Get("app"),
			ServiceName: qp.Get("service"),
			Query:       qp.Get("q"),
			Stream:      qp.Get("stream"),
			BeforeID:    before,
			Limit:       limit,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not search logs")
			return
		}
		out := make([]searchLogDTO, 0, len(rows))
		for _, l := range rows {
			out = append(out, searchLogDTO{
				ID:      l.ID,
				AppID:   l.AppID,
				Service: l.ServiceName,
				Stream:  l.Stream,
				Message: l.Message,
				At:      l.Timestamp,
			})
		}
		// A full page implies more rows may exist; hand back the oldest id as
		// the next cursor. A short page is the tail — signal end with 0.
		var next int64
		if len(rows) == limit {
			next = rows[len(rows)-1].ID
		}
		WriteJSON(w, http.StatusOK, searchLogsResponse{Logs: out, NextBefore: next})
	}
}
