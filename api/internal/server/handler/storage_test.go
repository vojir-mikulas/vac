package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStorageReader struct {
	usage []store.VolumeUsage
	apps  []store.App
}

func (f fakeStorageReader) ListVolumeUsage(context.Context) ([]store.VolumeUsage, error) {
	return f.usage, nil
}
func (f fakeStorageReader) ListApps(context.Context) ([]store.App, error) { return f.apps, nil }

type fakeDiskReporter struct{}

func (fakeDiskReporter) SystemDF(context.Context) (dockercli.DiskUsage, error) {
	return dockercli.DiskUsage{Images: dockercli.DiskUsageEntry{SizeBytes: 100}}, nil
}

func ptrI64(v int64) *int64 { return &v }
func ptrInt(v int) *int     { return &v }

func TestInstanceStorage_AggregatesAndSorts(t *testing.T) {
	measured := ptrI64
	reader := fakeStorageReader{
		apps: []store.App{
			{ID: "a1", Name: "Blog", Slug: "blog", DiskLimitMB: ptrInt(100)}, // 100 MiB limit
			{ID: "a2", Name: "Wiki", Slug: "wiki"},                           // no limit
		},
		usage: []store.VolumeUsage{
			// Blog: one measured (1000) + one unmeasured (nil) → total 1000, unmeasured 1.
			{AppID: "a1", AppSlug: "blog", MountPath: "/data", Source: "named", UsedBytes: measured(1000)},
			{AppID: "a1", AppSlug: "blog", MountPath: "/logs", Source: "bind", UsedBytes: nil},
			// Wiki: two measured → total 5000, the heavier app.
			{AppID: "a2", AppSlug: "wiki", MountPath: "/data", Source: "named", UsedBytes: measured(2000)},
			{AppID: "a2", AppSlug: "wiki", MountPath: "/media", Source: "named", UsedBytes: measured(3000)},
		},
	}

	rr := httptest.NewRecorder()
	InstanceStorage(reader, fakeDiskReporter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instance/storage", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp storageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := resp.Host.Images.SizeBytes; got != 100 {
		t.Errorf("host images size = %d, want 100", got)
	}
	if len(resp.Apps) != 2 {
		t.Fatalf("apps len = %d, want 2", len(resp.Apps))
	}

	// Heaviest first: wiki (5000) before blog (1000).
	wiki, blog := resp.Apps[0], resp.Apps[1]
	if wiki.Slug != "wiki" || blog.Slug != "blog" {
		t.Fatalf("sort order = [%s,%s], want [wiki,blog]", wiki.Slug, blog.Slug)
	}
	if wiki.UsedBytes != 5000 || wiki.VolumeCount != 2 || wiki.UnmeasuredCount != 0 {
		t.Errorf("wiki = %+v, want used 5000, count 2, unmeasured 0", wiki)
	}
	if wiki.LimitBytes != nil {
		t.Errorf("wiki limit = %v, want nil", wiki.LimitBytes)
	}
	if blog.UsedBytes != 1000 || blog.VolumeCount != 2 || blog.UnmeasuredCount != 1 {
		t.Errorf("blog = %+v, want used 1000 (measured only), count 2, unmeasured 1", blog)
	}
	if blog.ID != "a1" {
		t.Errorf("blog id = %q, want a1 (needed for the detail link)", blog.ID)
	}
	if blog.LimitBytes == nil || *blog.LimitBytes != 100*mib {
		t.Errorf("blog limit = %v, want %d", blog.LimitBytes, 100*mib)
	}
}
