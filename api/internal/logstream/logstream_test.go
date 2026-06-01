package logstream

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeSrc struct {
	lines chan dockercli.LogLine
}

func (f *fakeSrc) Logs(_ context.Context, _ string, _ time.Time) (<-chan dockercli.LogLine, error) {
	return f.lines, nil
}

type fakeSink struct {
	mu    sync.Mutex
	rows  []store.RuntimeLogRow
	trims int
}

func (f *fakeSink) AppendRuntimeLogs(_ context.Context, _ string, rows []store.RuntimeLogRow) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := int64(len(f.rows))
	f.rows = append(f.rows, rows...)
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = base + int64(i) + 1
	}
	return ids, nil
}

func (f *fakeSink) TrimRuntimeLogsToRingBuffer(_ context.Context, _, _ string, _ int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trims++
	return 0, nil
}

func (f *fakeSink) rowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakePub struct {
	mu     sync.Mutex
	frames [][]byte
}

func (f *fakePub) Publish(_ string, msg []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, msg)
}

func (f *fakePub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

func TestFollowerCapturesAndPublishes(t *testing.T) {
	lines := make(chan dockercli.LogLine, 4)
	sink := &fakeSink{}
	pub := &fakePub{}
	f := &follower{
		src: &fakeSrc{lines: lines}, sink: sink, pub: pub,
		appID: "a1", service: "web", container: "c1",
		ringBuffer: 100, flushEvery: 15 * time.Millisecond, trimEvery: time.Hour, maxBatch: 200,
		logger: slog.Default(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.run(ctx); close(done) }()

	lines <- dockercli.LogLine{Stream: "stdout", Message: "hello"}
	lines <- dockercli.LogLine{Stream: "stderr", Message: "oops"}

	// Wait for both the sink write and the hub publish to land — the publish
	// trails the sink write within a flush, so polling only the sink races the
	// pub assertion (visible under -race's slowdown).
	deadline := time.After(2 * time.Second)
	for sink.rowCount() < 2 || pub.count() < 2 {
		select {
		case <-deadline:
			t.Fatalf("captured %d rows / published %d frames, want 2/2", sink.rowCount(), pub.count())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// fakeStore implements SupervisorStore + Sink.
type fakeStore struct {
	fakeSink
	mu       sync.Mutex
	services []store.Service
}

func (f *fakeStore) ListApps(_ context.Context) ([]store.App, error) {
	return []store.App{{ID: "a1", Slug: "myapp"}}, nil
}

func (f *fakeStore) GetAppBySlug(_ context.Context, _ string) (store.App, error) {
	return store.App{ID: "a1", Slug: "myapp"}, nil
}

func (f *fakeStore) ListServicesForApp(_ context.Context, _ string) ([]store.Service, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Service(nil), f.services...), nil
}

func (f *fakeStore) setServices(svcs []store.Service) {
	f.mu.Lock()
	f.services = svcs
	f.mu.Unlock()
}

func svc(name, containerID, status string) store.Service {
	return store.Service{ServiceName: name, ContainerID: &containerID, Status: status}
}

func TestSupervisorReconcileStartsAndStopsFollowers(t *testing.T) {
	st := &fakeStore{}
	st.setServices([]store.Service{svc("web", "c1", deploy.ServiceStatusRunning)})
	sup := New(&fakeSrc{lines: make(chan dockercli.LogLine)}, st, st, nil, nil, Config{}, slog.Default())
	sup.parentCtx = context.Background()

	sup.ReconcileApp(context.Background(), "a1")
	if sup.count() != 1 {
		t.Fatalf("followers = %d, want 1 after running service appears", sup.count())
	}

	// Idempotent: a second reconcile with the same state changes nothing.
	sup.ReconcileApp(context.Background(), "a1")
	if sup.count() != 1 {
		t.Fatalf("followers = %d, want 1 (idempotent)", sup.count())
	}

	// Container gone → follower cancelled.
	st.setServices([]store.Service{{ServiceName: "web", Status: deploy.ServiceStatusStopped}})
	sup.ReconcileApp(context.Background(), "a1")
	if sup.count() != 0 {
		t.Fatalf("followers = %d, want 0 after container vanished", sup.count())
	}
}

func TestSupervisorSkipsPortlessAndStopped(t *testing.T) {
	st := &fakeStore{}
	st.setServices([]store.Service{
		svc("worker", "c2", deploy.ServiceStatusStopped),         // stopped → skip
		{ServiceName: "db", Status: deploy.ServiceStatusRunning}, // no container id → skip
	})
	sup := New(&fakeSrc{lines: make(chan dockercli.LogLine)}, st, st, nil, nil, Config{}, slog.Default())
	sup.parentCtx = context.Background()
	sup.ReconcileApp(context.Background(), "a1")
	if sup.count() != 0 {
		t.Fatalf("followers = %d, want 0", sup.count())
	}
}
