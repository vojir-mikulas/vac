package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// maxSince caps how far back a metrics query can reach — the window is only
// retained for 24h anyway.
const maxSince = 24 * time.Hour

// AppMetrics returns the request-rate series summed across the app's services.
func AppMetrics(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		since := parseSince(r)
		series, err := s.QueryRequestSeries(r.Context(), appID, "", since)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load metrics")
			return
		}
		writeSeries(w, series)
	}
}

// ServiceMetrics returns the request-rate series for one service.
func ServiceMetrics(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")
		since := parseSince(r)
		series, err := s.QueryRequestSeries(r.Context(), appID, name, since)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load metrics")
			return
		}
		writeSeries(w, series)
	}
}

// hostSeriesBuckets is the target number of points the host series is
// downsampled to, regardless of window — keeps the dashboard sparkline light.
const hostSeriesBuckets = 48

// HostMetrics returns the request series summed across every app, for the
// dashboard's box-wide traffic sparkline. The window is downsampled to a fixed
// number of buckets so a 24h range stays a few dozen points.
func HostMetrics(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := parseSince(r)
		// Bucket width = window / target points, floored at 60s (the access-log
		// aggregator writes 10s buckets, so anything finer just re-splits them).
		bucketSec := int(time.Since(since).Seconds()) / hostSeriesBuckets
		if bucketSec < 60 {
			bucketSec = 60
		}
		series, err := s.QueryHostRequestSeries(r.Context(), since, bucketSec)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load metrics")
			return
		}
		writeSeries(w, series)
	}
}

func writeSeries(w http.ResponseWriter, series []store.RequestPoint) {
	if series == nil {
		series = []store.RequestPoint{}
	}
	WriteJSON(w, http.StatusOK, series)
}

// parseSince reads ?since=<duration> (default 1h), clamped to [0, 24h] ago.
func parseSince(r *http.Request) time.Time {
	window := time.Hour
	if v := r.URL.Query().Get("since"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			window = d
		}
	}
	if window > maxSince {
		window = maxSince
	}
	return time.Now().Add(-window)
}
