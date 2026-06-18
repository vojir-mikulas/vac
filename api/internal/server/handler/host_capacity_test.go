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

type fakeCapacityProvider struct {
	totalBytes uint64
	samples    []stats.AppSample
}

func (f fakeCapacityProvider) Snapshot(context.Context) stats.HostSnapshot {
	return stats.HostSnapshot{MemTotalBytes: f.totalBytes}
}

func (f fakeCapacityProvider) SnapshotAll(context.Context) []stats.AppSample { return f.samples }

type fakeCapacityStore struct {
	alloc store.MemAllocation
	apps  []store.App
}

func (f fakeCapacityStore) SumAppMemLimits(context.Context) (store.MemAllocation, error) {
	return f.alloc, nil
}

func (f fakeCapacityStore) ListApps(context.Context) ([]store.App, error) { return f.apps, nil }

func TestHostCapacity(t *testing.T) {
	const mib = 1024 * 1024
	provider := fakeCapacityProvider{
		totalBytes: 2048 * mib,
		samples: []stats.AppSample{
			{App: "web", Service: "app", MemBytes: 100 * mib},
			{App: "web", Service: "worker", MemBytes: 50 * mib}, // summed back to "web"
			{App: "api", Service: "app", MemBytes: 300 * mib},
		},
	}
	st := fakeCapacityStore{
		alloc: store.MemAllocation{AllocatedMB: 1024, AppsWithLimit: 2, AppsTotal: 3},
		apps: []store.App{
			{Slug: "web", Name: "web", MemLimitMB: ptrInt(512)},
			{Slug: "api", Name: "api", MemLimitMB: ptrInt(512)},
			{Slug: "idle", Name: "idle", MemLimitMB: nil}, // unbudgeted + not in snapshot
		},
	}

	h := HostCapacity(provider, st)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/host/capacity", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got capacityDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got.TotalRAMMB != 2048 || got.AllocatedMB != 1024 || got.OverCommitted {
		t.Errorf("totals = %+v, want total=2048 allocated=1024 over=false", got)
	}
	if len(got.Apps) != 3 {
		t.Fatalf("apps len = %d, want 3", len(got.Apps))
	}
	// Heaviest live app first.
	if got.Apps[0].Slug != "api" || got.Apps[0].ActualMemBytes != 300*mib || !got.Apps[0].Running {
		t.Errorf("apps[0] = %+v, want api 300MiB running", got.Apps[0])
	}
	// "web" sums its two services.
	if got.Apps[1].Slug != "web" || got.Apps[1].ActualMemBytes != 150*mib {
		t.Errorf("apps[1] = %+v, want web 150MiB", got.Apps[1])
	}
	// An app with no live container reads zero and not-running, and keeps its nil cap.
	last := got.Apps[2]
	if last.Slug != "idle" || last.ActualMemBytes != 0 || last.Running || last.MemLimitMB != nil {
		t.Errorf("apps[2] = %+v, want idle 0 not-running unlimited", last)
	}
}

func TestHostCapacityOverCommitted(t *testing.T) {
	const mib = 1024 * 1024
	h := HostCapacity(
		fakeCapacityProvider{totalBytes: 1024 * mib},
		fakeCapacityStore{alloc: store.MemAllocation{AllocatedMB: 2048, AppsWithLimit: 4, AppsTotal: 4}},
	)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/host/capacity", nil))
	var got capacityDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OverCommitted {
		t.Errorf("over_committed = false, want true (2048 > 1024)")
	}
}
