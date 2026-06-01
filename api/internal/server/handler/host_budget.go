package handler

import (
	"context"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// BudgetStore is the slice of *store.Store the box-budget endpoint reads.
type BudgetStore interface {
	SumAppMemLimits(ctx context.Context) (store.MemAllocation, error)
}

type budgetDTO struct {
	TotalRAMMB    int64 `json:"total_ram_mb"`
	AllocatedMB   int64 `json:"allocated_mb"`
	AppsWithLimit int   `json:"apps_with_limit"`
	AppsTotal     int   `json:"apps_total"`
	OverCommitted bool  `json:"over_committed"`
}

// HostBudget reports how much of the box's RAM apps have reserved via per-app
// limits, for the dashboard's container-budget panel (plan 06). "Allocated" sums
// the per-app limits; apps without a limit aren't budgeted (counted via
// apps_total − apps_with_limit) and the UI can warn about them. Over-commit is a
// soft signal — VAC never blocks a deploy on it.
func HostBudget(provider HostStatsProvider, s BudgetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alloc, err := s.SumAppMemLimits(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read allocation")
			return
		}
		totalMB := int64(provider.Snapshot(r.Context()).MemTotalBytes / (1024 * 1024))
		WriteJSON(w, http.StatusOK, budgetDTO{
			TotalRAMMB:    totalMB,
			AllocatedMB:   alloc.AllocatedMB,
			AppsWithLimit: alloc.AppsWithLimit,
			AppsTotal:     alloc.AppsTotal,
			OverCommitted: totalMB > 0 && alloc.AllocatedMB > totalMB,
		})
	}
}
