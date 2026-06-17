package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// volumeDTO is one mount's latest usage sample on the wire. UsedBytes is null when
// the mount has not been measured yet (a skipped/timed-out bind-mount walk) so the
// UI can show "not measured" instead of a false 0. LimitBytes echoes the app's
// soft disk budget (per-app, repeated on each row) so the client can draw a fill
// bar without a second request; null when no budget is set.
type volumeDTO struct {
	Service    string    `json:"service"`
	Volume     string    `json:"volume"`
	MountPath  string    `json:"mount_path"`
	Source     string    `json:"source"`
	UsedBytes  *int64    `json:"used_bytes"`
	LimitBytes *int64    `json:"limit_bytes"`
	SampledAt  time.Time `json:"sampled_at"`
}

type volumesResponse struct {
	Volumes []volumeDTO `json:"volumes"`
}

// GetAppVolumes returns the latest persisted volume-usage snapshot for an app
// (REST snapshot, not a WS topic — usage changes on the order of minutes).
func GetAppVolumes(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		var limit *int64
		if app.DiskLimitMB != nil {
			l := int64(*app.DiskLimitMB) * 1024 * 1024
			limit = &l
		}
		rows, err := s.ListVolumeUsageByApp(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load volumes")
			return
		}
		out := volumesResponse{Volumes: make([]volumeDTO, 0, len(rows))}
		for _, v := range rows {
			out.Volumes = append(out.Volumes, volumeDTO{
				Service:    v.ServiceName,
				Volume:     v.VolumeName,
				MountPath:  v.MountPath,
				Source:     v.Source,
				UsedBytes:  v.UsedBytes,
				LimitBytes: limit,
				SampledAt:  v.SampledAt,
			})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}
