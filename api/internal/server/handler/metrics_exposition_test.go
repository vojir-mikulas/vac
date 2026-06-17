package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/stats"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStatsProvider struct{}

func (fakeStatsProvider) Snapshot(context.Context) stats.HostSnapshot {
	return stats.HostSnapshot{CPUPercent: 1.5, MemUsedBytes: 50, MemTotalBytes: 200}
}

func (fakeStatsProvider) SnapshotAll(context.Context) []stats.AppSample {
	return []stats.AppSample{{App: "blog", Service: "web", CPUPercent: 2, MemBytes: 4096}}
}

type fakeMetricsStore struct{ fail bool }

func (f fakeMetricsStore) CountDeploymentsByStatus(context.Context) ([]store.DeployStatusCount, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	return []store.DeployStatusCount{{Slug: "blog", Status: "success", TriggeredBy: "manual", Count: 3}}, nil
}

func (f fakeMetricsStore) LatestDeployDurations(context.Context) ([]store.DeployDuration, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	return []store.DeployDuration{{Slug: "blog", Seconds: 12.5}}, nil
}

func (f fakeMetricsStore) SumRequestMetrics(context.Context, time.Time) ([]store.RequestTotal, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	return []store.RequestTotal{{Slug: "blog", Service: "web", Requests: 9, Errors: 1}}, nil
}

func (f fakeMetricsStore) ListVolumeUsage(context.Context) ([]store.VolumeUsage, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	used := int64(824567321)
	return []store.VolumeUsage{{AppSlug: "blog", ServiceName: "db", VolumeName: "vac-blog_pgdata", UsedBytes: &used}}, nil
}

func TestMetricsExposition_RendersAllSections(t *testing.T) {
	h := MetricsExposition(fakeStatsProvider{}, fakeMetricsStore{}, time.Hour, "v9", "deadbeef")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain…", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"vac_host_cpu_percent 1.5",
		`vac_app_mem_bytes{app="blog",service="web"} 4096`,
		`vac_deploys_total{app="blog",status="success",triggered_by="manual"} 3`,
		`vac_deploy_duration_seconds{app="blog"} 12.5`,
		`vac_requests_total{app="blog",service="web"} 9`,
		`vac_build_info{version="v9",commit="deadbeef"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestMetricsExposition_DegradesOnStoreError(t *testing.T) {
	// A failing store should drop the persisted sections but still serve host +
	// build metrics with a 200 — a scrape must not fail wholesale.
	h := MetricsExposition(fakeStatsProvider{}, fakeMetricsStore{fail: true}, time.Hour, "v9", "c0")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "vac_host_cpu_percent 1.5") {
		t.Fatalf("host metric missing on degraded scrape:\n%s", body)
	}
	if strings.Contains(body, "vac_deploys_total{") {
		t.Fatalf("expected no deploy samples when store fails:\n%s", body)
	}
}
