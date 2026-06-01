package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/stats"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeBudgetStats struct{ totalBytes uint64 }

func (f fakeBudgetStats) Snapshot(context.Context) stats.HostSnapshot {
	return stats.HostSnapshot{MemTotalBytes: f.totalBytes}
}

type fakeBudgetStore struct{ alloc store.MemAllocation }

func (f fakeBudgetStore) SumAppMemLimits(context.Context) (store.MemAllocation, error) {
	return f.alloc, nil
}

func TestHostBudget(t *testing.T) {
	const mib = 1024 * 1024
	cases := map[string]struct {
		totalMB       uint64 // host total, in MiB (converted to bytes below)
		alloc         store.MemAllocation
		wantOver      bool
		wantAllocated int64
	}{
		"within budget":  {totalMB: 2048, alloc: store.MemAllocation{AllocatedMB: 1024, AppsWithLimit: 2, AppsTotal: 3}, wantOver: false, wantAllocated: 1024},
		"over committed": {totalMB: 1024, alloc: store.MemAllocation{AllocatedMB: 2048, AppsWithLimit: 4, AppsTotal: 4}, wantOver: true, wantAllocated: 2048},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := HostBudget(fakeBudgetStats{totalBytes: tc.totalMB * mib}, fakeBudgetStore{alloc: tc.alloc})
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/host/budget", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			var got budgetDTO
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.OverCommitted != tc.wantOver {
				t.Errorf("over_committed = %v, want %v", got.OverCommitted, tc.wantOver)
			}
			if got.AllocatedMB != tc.wantAllocated {
				t.Errorf("allocated_mb = %d, want %d", got.AllocatedMB, tc.wantAllocated)
			}
			if got.TotalRAMMB != int64(tc.totalMB) {
				t.Errorf("total_ram_mb = %d, want %d", got.TotalRAMMB, tc.totalMB)
			}
		})
	}
}
