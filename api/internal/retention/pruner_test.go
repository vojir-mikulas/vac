package retention_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/retention"
)

type fakeStore struct {
	calls      atomic.Int64
	last       time.Time
	rmCalls    atomic.Int64
	rmLast     time.Time
	auditCalls atomic.Int64
	auditLast  time.Time
	trimCalls  atomic.Int64
	trimKeep   int

	serviceProjects []struct{ Slug, ServiceName string }
	deployCalls     atomic.Int64
	deployKeep      int
}

func (f *fakeStore) DeleteRuntimeLogsOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.calls.Add(1)
	f.last = cutoff
	return 42, nil
}

func (f *fakeStore) DeleteRequestMetricsOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.rmCalls.Add(1)
	f.rmLast = cutoff
	return 7, nil
}

func (f *fakeStore) DeleteAuditLogOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.auditCalls.Add(1)
	f.auditLast = cutoff
	return 5, nil
}

func (f *fakeStore) ListRuntimeLogServices(_ context.Context) ([]struct{ AppID, ServiceName string }, error) {
	return []struct{ AppID, ServiceName string }{{AppID: "app-1", ServiceName: "web"}}, nil
}

func (f *fakeStore) TrimRuntimeLogsToRingBuffer(_ context.Context, _, _ string, keepN int) (int64, error) {
	f.trimCalls.Add(1)
	f.trimKeep = keepN
	return 3, nil
}

func (f *fakeStore) ListServiceProjects(_ context.Context) ([]struct{ Slug, ServiceName string }, error) {
	return f.serviceProjects, nil
}

func (f *fakeStore) PruneDeployments(_ context.Context, keepN int) (int64, error) {
	f.deployCalls.Add(1)
	f.deployKeep = keepN
	return 4, nil
}

func TestPruneOnce_ComputesCutoffFromRuntimeDays(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, nil, retention.Config{RuntimeDays: 7}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatalf("PruneOnce: %v", err)
	}
	if s.calls.Load() != 1 {
		t.Errorf("delete calls = %d, want 1", s.calls.Load())
	}
	// Cutoff should be approximately 7 days ago.
	diff := time.Since(s.last)
	if diff < (7*24*time.Hour-time.Minute) || diff > (7*24*time.Hour+time.Minute) {
		t.Errorf("cutoff diff = %v, want ~7 days", diff)
	}
}

func TestPruneOnce_ComputesAuditCutoffFromActivityDays(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, nil, retention.Config{ActivityDays: 30}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatalf("PruneOnce: %v", err)
	}
	if s.auditCalls.Load() != 1 {
		t.Errorf("audit delete calls = %d, want 1", s.auditCalls.Load())
	}
	diff := time.Since(s.auditLast)
	if diff < (30*24*time.Hour-time.Minute) || diff > (30*24*time.Hour+time.Minute) {
		t.Errorf("audit cutoff diff = %v, want ~30 days", diff)
	}
}

func TestPruneOnce_DefaultsTo7Days(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, nil, retention.Config{}, nil) // empty config
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	diff := time.Since(s.last)
	if diff < (7*24*time.Hour-time.Minute) || diff > (7*24*time.Hour+time.Minute) {
		t.Errorf("default cutoff diff = %v, want ~7 days", diff)
	}
}

func TestPruneOnce_PrunesDeploymentsWithConfiguredKeep(t *testing.T) {
	s := &fakeStore{}
	p := retention.New(s, nil, retention.Config{DeploymentKeepCount: 12}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.deployCalls.Load() != 1 {
		t.Errorf("PruneDeployments calls = %d, want 1", s.deployCalls.Load())
	}
	if s.deployKeep != 12 {
		t.Errorf("deployment keepN = %d, want 12", s.deployKeep)
	}
}

// fakeImagePruner records RemoveImage calls and serves a fixed image list per
// (project, service).
type fakeImagePruner struct {
	byKey   map[string][]dockercli.Image
	removed []string
	inUse   map[string]bool // ids that fail to remove ("image in use")
}

func (f *fakeImagePruner) ListImages(_ context.Context, project, service string) ([]dockercli.Image, error) {
	return f.byKey[project+"/"+service], nil
}

func (f *fakeImagePruner) RemoveImage(_ context.Context, id string) error {
	if f.inUse[id] {
		return errors.New("image is in use by running container")
	}
	f.removed = append(f.removed, id)
	return nil
}

func TestPruneImages_KeepsNewestNAndIgnoresInUse(t *testing.T) {
	s := &fakeStore{serviceProjects: []struct{ Slug, ServiceName string }{{Slug: "blog", ServiceName: "web"}}}
	img := func(id, created string) dockercli.Image {
		return dockercli.Image{ID: id, CreatedAt: created}
	}
	fip := &fakeImagePruner{
		byKey: map[string][]dockercli.Image{
			// Deliberately unsorted; newest is img-4.
			"vac-blog/web": {
				img("img-1", "2024-01-01 10:00:00 +0000 UTC"),
				img("img-4", "2024-04-01 10:00:00 +0000 UTC"),
				img("img-2", "2024-02-01 10:00:00 +0000 UTC"),
				img("img-3", "2024-03-01 10:00:00 +0000 UTC"),
			},
		},
		inUse: map[string]bool{"img-2": true}, // simulate the live image refusing removal
	}
	p := retention.New(s, fip, retention.Config{ImageKeepCount: 2}, nil)
	if err := p.PruneOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Keep newest 2 (img-4, img-3); attempt to remove img-2 (in-use → skipped)
	// and img-1 (removed). So only img-1 lands in removed.
	if len(fip.removed) != 1 || fip.removed[0] != "img-1" {
		t.Errorf("removed = %v, want [img-1] (img-2 is in-use and skipped)", fip.removed)
	}
}
