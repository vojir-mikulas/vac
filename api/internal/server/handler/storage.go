package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// StorageReader is the box-wide read side the Storage page needs: every volume
// sample (already joined to its app slug) plus the app list for names and soft
// disk budgets. *store.Store satisfies it.
type StorageReader interface {
	ListVolumeUsage(ctx context.Context) ([]store.VolumeUsage, error)
	ListApps(ctx context.Context) ([]store.App, error)
}

const mib = 1024 * 1024

// appStorage is one row of the fleet-wide Storage page: an app's aggregated
// volume usage. UsedBytes sums only measured mounts; UnmeasuredCount reports how
// many mounts had no sample (a skipped/timed-out bind-mount walk), so the UI can
// render the total as a floor rather than a false 0.
type appStorage struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	UsedBytes       int64  `json:"used_bytes"`
	VolumeCount     int    `json:"volume_count"`
	UnmeasuredCount int    `json:"unmeasured_count"`
	LimitBytes      *int64 `json:"limit_bytes"` // disk_limit_mb in bytes; nil = no soft limit
}

type storageResponse struct {
	Apps []appStorage        `json:"apps"`
	Host dockercli.DiskUsage `json:"host"`
}

// InstanceStorage answers "what is filling this box": per-app volume totals
// (aggregated from the collector's persisted samples) sorted by usage, plus the
// host's docker disk breakdown for the same one-request page. Aggregation happens
// in Go over the existing box-wide query so no new store method or migration is
// needed.
//
// GET /api/instance/storage
func InstanceStorage(r StorageReader, d DiskReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		usage, err := r.ListVolumeUsage(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read volume usage")
			return
		}
		apps, err := r.ListApps(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read apps")
			return
		}
		host, err := d.SystemDF(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read disk usage")
			return
		}

		// App metadata (name + soft limit) keyed by id; the volume query only
		// carries the slug.
		type meta struct {
			name  string
			limit *int64
		}
		byID := make(map[string]meta, len(apps))
		for _, a := range apps {
			var limit *int64
			if a.DiskLimitMB != nil {
				b := int64(*a.DiskLimitMB) * mib
				limit = &b
			}
			byID[a.ID] = meta{name: a.Name, limit: limit}
		}

		// Aggregate samples per app. The volume_usage table only holds rows for
		// apps that actually have mounts, so stateless apps never appear.
		agg := make(map[string]*appStorage)
		for _, v := range usage {
			row := agg[v.AppID]
			if row == nil {
				m := byID[v.AppID] // zero value (name "", nil limit) if app vanished
				name := m.name
				if name == "" {
					name = v.AppSlug
				}
				row = &appStorage{ID: v.AppID, Slug: v.AppSlug, Name: name, LimitBytes: m.limit}
				agg[v.AppID] = row
			}
			row.VolumeCount++
			if v.UsedBytes != nil {
				row.UsedBytes += *v.UsedBytes
			} else {
				row.UnmeasuredCount++
			}
		}

		out := make([]appStorage, 0, len(agg))
		for _, row := range agg {
			out = append(out, *row)
		}
		// Heaviest first; tie-break by slug so the order is stable across polls.
		sort.Slice(out, func(i, j int) bool {
			if out[i].UsedBytes != out[j].UsedBytes {
				return out[i].UsedBytes > out[j].UsedBytes
			}
			return out[i].Slug < out[j].Slug
		})

		WriteJSON(w, http.StatusOK, storageResponse{Apps: out, Host: host})
	}
}
