package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// CapacityStore is the slice of *store.Store the capacity endpoint reads:
// the committed-RAM aggregate plus the app list to break it down per app.
type CapacityStore interface {
	SumAppMemLimits(ctx context.Context) (store.MemAllocation, error)
	ListApps(ctx context.Context) ([]store.App, error)
}

type capacityAppDTO struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	MemLimitMB     *int   `json:"mem_limit_mb"`     // nil = unlimited (unbudgeted)
	ActualMemBytes int64  `json:"actual_mem_bytes"` // live, summed across the app's services
	Running        bool   `json:"running"`          // has a live container in the snapshot
}

type capacityDTO struct {
	TotalRAMMB    int64            `json:"total_ram_mb"`
	AllocatedMB   int64            `json:"allocated_mb"`
	AppsWithLimit int              `json:"apps_with_limit"`
	AppsTotal     int              `json:"apps_total"`
	OverCommitted bool             `json:"over_committed"`
	Apps          []capacityAppDTO `json:"apps"`
}

// HostCapacity is the per-app breakdown behind the dashboard budget panel: each
// app's committed RAM cap (nil = unlimited) alongside its live actual usage, plus
// the same box totals the budget endpoint reports. "Committed" sums per-app caps
// across all apps (including stopped ones — a reservation persists); "actual" is a
// one-shot `docker stats` poll (StatsProvider.SnapshotAll), so it carries ~zero
// idle cost — nothing samples RAM unless this endpoint is hit. A snapshot poll
// failure degrades to actual=0 / running=false rather than failing the response.
func HostCapacity(provider StatsProvider, s CapacityStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		alloc, err := s.SumAppMemLimits(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read allocation")
			return
		}
		apps, err := s.ListApps(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list apps")
			return
		}
		// Sum live per-service usage back up to the owning app (keyed by slug, the
		// label SnapshotAll carries).
		actualBySlug := map[string]int64{}
		for _, smp := range provider.SnapshotAll(ctx) {
			actualBySlug[smp.App] += smp.MemBytes
		}
		totalMB := int64(provider.Snapshot(ctx).MemTotalBytes / (1024 * 1024))

		out := make([]capacityAppDTO, 0, len(apps))
		for _, a := range apps {
			actual, running := actualBySlug[a.Slug]
			out = append(out, capacityAppDTO{
				Slug:           a.Slug,
				Name:           a.Name,
				MemLimitMB:     a.MemLimitMB,
				ActualMemBytes: actual,
				Running:        running,
			})
		}
		// Heaviest live apps first, then alphabetical — the operator scanning for
		// what to cap wants the biggest consumers at the top.
		sort.Slice(out, func(i, j int) bool {
			if out[i].ActualMemBytes != out[j].ActualMemBytes {
				return out[i].ActualMemBytes > out[j].ActualMemBytes
			}
			return out[i].Name < out[j].Name
		})

		WriteJSON(w, http.StatusOK, capacityDTO{
			TotalRAMMB:    totalMB,
			AllocatedMB:   alloc.AllocatedMB,
			AppsWithLimit: alloc.AppsWithLimit,
			AppsTotal:     alloc.AppsTotal,
			OverCommitted: totalMB > 0 && alloc.AllocatedMB > totalMB,
			Apps:          out,
		})
	}
}
