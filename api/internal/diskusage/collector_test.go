package diskusage

import (
	"context"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStore struct {
	apps     []store.App
	services map[string][]store.Service
	upserts  []store.VolumeUsage
	pruned   map[string][]string
}

func (f *fakeStore) ListApps(context.Context) ([]store.App, error) { return f.apps, nil }
func (f *fakeStore) ListServicesForApp(_ context.Context, appID string) ([]store.Service, error) {
	return f.services[appID], nil
}
func (f *fakeStore) UpsertVolumeUsage(_ context.Context, v store.VolumeUsage) error {
	f.upserts = append(f.upserts, v)
	return nil
}
func (f *fakeStore) DeleteVolumeUsageForAppExcept(_ context.Context, appID string, keep []string) error {
	if f.pruned == nil {
		f.pruned = map[string][]string{}
	}
	f.pruned[appID] = keep
	return nil
}

type fakeDocker struct {
	sizes  map[string]int64
	mounts map[string][]dockercli.Mount
}

func (f *fakeDocker) VolumeSizes(context.Context) (map[string]int64, error) { return f.sizes, nil }
func (f *fakeDocker) ContainerMounts(_ context.Context, id string) ([]dockercli.Mount, error) {
	return f.mounts[id], nil
}

type fakeNotifier struct {
	calls []string // scope of each DiskUsageHigh
}

func (f *fakeNotifier) DiskUsageHigh(_, _, scope, _ string) { f.calls = append(f.calls, scope) }

func cid(s string) *string { return &s }
func mb(n int) *int        { return &n }

func newTestCollector(s Store, d Docker, n Notifier, host HostDisk) *Collector {
	c := New(s, d, n, host, Config{Interval: time.Minute, AlertPercent: 85, Cooldown: time.Hour}, nil)
	return c
}

func TestCollectOnce_MapsNamedVolumeAndPrunes(t *testing.T) {
	st := &fakeStore{
		apps: []store.App{{ID: "a1", Slug: "blog", Name: "Blog"}},
		services: map[string][]store.Service{
			"a1": {{ServiceName: "db", ContainerID: cid("c1"), HasVolumes: true}},
		},
	}
	dk := &fakeDocker{
		sizes: map[string]int64{"vac-blog_pgdata": 1000},
		mounts: map[string][]dockercli.Mount{
			"c1": {
				{Type: "volume", Name: "vac-blog_pgdata", Destination: "/var/lib/postgresql/data"},
				{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"},
			},
		},
	}
	c := newTestCollector(st, dk, nil, nil)
	c.collectOnce(context.Background())

	if len(st.upserts) != 1 {
		t.Fatalf("want 1 upsert (socket bind skipped), got %d", len(st.upserts))
	}
	got := st.upserts[0]
	if got.Source != "named" || got.VolumeName != "vac-blog_pgdata" || got.UsedBytes == nil || *got.UsedBytes != 1000 {
		t.Fatalf("unexpected upsert: %+v", got)
	}
	if keep := st.pruned["a1"]; len(keep) != 1 || keep[0] != "/var/lib/postgresql/data" {
		t.Fatalf("prune keep-set wrong: %v", keep)
	}
}

func TestCollectOnce_BindNotMeasuredWhenScanOff(t *testing.T) {
	st := &fakeStore{
		apps:     []store.App{{ID: "a1", Slug: "blog"}},
		services: map[string][]store.Service{"a1": {{ServiceName: "web", ContainerID: cid("c1"), HasVolumes: true}}},
	}
	dk := &fakeDocker{mounts: map[string][]dockercli.Mount{
		"c1": {{Type: "bind", Source: "/mnt/hdd/data", Destination: "/data"}},
	}}
	c := newTestCollector(st, dk, nil, nil)
	c.collectOnce(context.Background())
	if len(st.upserts) != 1 || st.upserts[0].Source != "bind" || st.upserts[0].UsedBytes != nil {
		t.Fatalf("bind mount should be recorded unmeasured: %+v", st.upserts)
	}
}

func TestEvalApp_FireCooldownRecover(t *testing.T) {
	app := store.App{ID: "a1", Slug: "blog", Name: "Blog", DiskLimitMB: mb(1)} // 1 MiB budget
	n := &fakeNotifier{}
	c := newTestCollector(&fakeStore{}, &fakeDocker{}, n, nil)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }

	over := int64(900 * 1024) // 900 KiB of 1 MiB = ~87% ≥ 85
	c.evalApp(app, over, true)
	if len(n.calls) != 1 {
		t.Fatalf("want 1 alert on first crossing, got %d", len(n.calls))
	}
	// Still high, within cooldown → suppressed.
	now = now.Add(30 * time.Minute)
	c.evalApp(app, over, true)
	if len(n.calls) != 1 {
		t.Fatalf("cooldown should suppress, got %d", len(n.calls))
	}
	// Recovers → re-arms (no alert).
	c.evalApp(app, int64(100*1024), true)
	if len(n.calls) != 1 {
		t.Fatalf("recovery should not alert, got %d", len(n.calls))
	}
	// Crosses again after re-arm → fires immediately despite cooldown window.
	c.evalApp(app, over, true)
	if len(n.calls) != 2 {
		t.Fatalf("re-crossing after recovery should alert, got %d", len(n.calls))
	}
}

func TestEvalApp_NoLimitOrUnmeasured(t *testing.T) {
	n := &fakeNotifier{}
	c := newTestCollector(&fakeStore{}, &fakeDocker{}, n, nil)
	c.evalApp(store.App{ID: "a1"}, 1<<30, true)                  // no budget
	c.evalApp(store.App{ID: "a2", DiskLimitMB: mb(1)}, 0, false) // unmeasured
	if len(n.calls) != 0 {
		t.Fatalf("no alert expected, got %d", len(n.calls))
	}
}

func TestEvalHost_FiresOverThreshold(t *testing.T) {
	n := &fakeNotifier{}
	host := func(context.Context) (uint64, uint64) { return 95, 100 }
	c := newTestCollector(&fakeStore{}, &fakeDocker{}, n, host)
	c.evalHost(context.Background())
	if len(n.calls) != 1 || n.calls[0] != "host disk" {
		t.Fatalf("want host alert, got %v", n.calls)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{512: "512 B", 1024: "1.0 KiB", 1048576: "1.0 MiB", 1610612736: "1.5 GiB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
