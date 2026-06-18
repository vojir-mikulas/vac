package scaletozero

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeStore is a configurable in-memory Store for the waker/sweeper tests.
type fakeStore struct {
	mu sync.Mutex

	apps        map[string]store.App // by id
	idleApps    []store.App          // returned by ListIdleSuspendApps
	services    []store.Service      // returned for any app
	lastTraffic map[string]time.Time // by app id
	suspendSet  map[string]bool      // recorded SetAppSuspended calls
	svcStatus   map[string]string    // serviceName -> last status written
	lastSet     map[string]time.Time // SetLastTrafficAt calls
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		apps:        map[string]store.App{},
		lastTraffic: map[string]time.Time{},
		suspendSet:  map[string]bool{},
		svcStatus:   map[string]string{},
		lastSet:     map[string]time.Time{},
	}
}

func (f *fakeStore) GetApp(_ context.Context, id string) (store.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[id]
	if !ok {
		return store.App{}, store.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) ListServicesForApp(_ context.Context, _ string) ([]store.Service, error) {
	return f.services, nil
}

func (f *fakeStore) UpdateServiceStatus(_ context.Context, _, name, status string, _ *int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.svcStatus[name] = status
	return nil
}

func (f *fakeStore) SetAppSuspended(_ context.Context, id string, suspended bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspendSet[id] = suspended
	a := f.apps[id]
	a.Suspended = suspended
	f.apps[id] = a
	return nil
}

func (f *fakeStore) SetLastTrafficAt(_ context.Context, id string, ts time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSet[id] = ts
	return nil
}

func (f *fakeStore) LastTrafficSince(_ context.Context, appID string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTraffic[appID], nil
}

func (f *fakeStore) ListIdleSuspendApps(_ context.Context) ([]store.App, error) {
	return f.idleApps, nil
}

// fakeCompose records stop/start and can block start to exercise the in-flight
// guard.
type fakeCompose struct {
	mu        sync.Mutex
	stops     int
	starts    int
	startGate chan struct{} // when non-nil, Start blocks until it's closed
	started   chan struct{} // closed-on-first-entry signal for tests
}

func (c *fakeCompose) Stop(_ context.Context, _, _ string) error {
	c.mu.Lock()
	c.stops++
	c.mu.Unlock()
	return nil
}

func (c *fakeCompose) Start(_ context.Context, _, _ string) error {
	c.mu.Lock()
	c.starts++
	c.mu.Unlock()
	if c.started != nil {
		select {
		case <-c.started:
		default:
			close(c.started)
		}
	}
	if c.startGate != nil {
		<-c.startGate
	}
	return nil
}

// fakeProxy records the wake-related proxy calls.
type fakeProxy struct {
	mu         sync.Mutex
	wakeRoutes int
	syncs      int
	healthErr  error
}

func (p *fakeProxy) InstallWakeRoutes(_ context.Context, _ string) error {
	p.mu.Lock()
	p.wakeRoutes++
	p.mu.Unlock()
	return nil
}

func (p *fakeProxy) Sync(_ context.Context, _ string) error {
	p.mu.Lock()
	p.syncs++
	p.mu.Unlock()
	return nil
}

func (p *fakeProxy) WaitHealthy(_ context.Context, _ string) error { return p.healthErr }

// ---- tests ----

func TestSuspend_StopsMarksAndInstallsWakeRoutes(t *testing.T) {
	st := newFakeStore()
	app := store.App{ID: "a1", Slug: "blog"}
	st.apps["a1"] = app
	st.services = []store.Service{{ServiceName: "web"}}
	dc := &fakeCompose{}
	px := &fakeProxy{}
	w := NewWaker(st, dc, px, nil)

	if err := w.Suspend(context.Background(), app, time.Now()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if dc.stops != 1 {
		t.Errorf("stops = %d, want 1", dc.stops)
	}
	if !st.suspendSet["a1"] {
		t.Errorf("app not marked suspended")
	}
	if px.wakeRoutes != 1 {
		t.Errorf("wake routes installed = %d, want 1", px.wakeRoutes)
	}
	if st.svcStatus["web"] != serviceStatusStopped {
		t.Errorf("service status = %q, want stopped", st.svcStatus["web"])
	}
}

func TestSuspend_SkipsWhenWokenSinceDecision(t *testing.T) {
	// A wake completed between the sweeper's listing and this Suspend: the app row
	// now carries a LastTrafficAt newer than the sweep tick. Suspend must back off
	// rather than re-stop the just-woken app.
	decidedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	wokenAt := decidedAt.Add(5 * time.Second)
	st := newFakeStore()
	app := store.App{ID: "a1", Slug: "blog"}
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", LastTrafficAt: &wokenAt}
	dc := &fakeCompose{}
	px := &fakeProxy{}
	w := NewWaker(st, dc, px, nil)

	if err := w.Suspend(context.Background(), app, decidedAt); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if dc.stops != 0 {
		t.Errorf("must not stop an app woken since the decision, stops = %d", dc.stops)
	}
}

func TestSuspend_SkipsWhenAlreadySuspended(t *testing.T) {
	st := newFakeStore()
	app := store.App{ID: "a1", Slug: "blog"}
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: true}
	dc := &fakeCompose{}
	w := NewWaker(st, dc, &fakeProxy{}, nil)

	if err := w.Suspend(context.Background(), app, time.Now()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if dc.stops != 0 {
		t.Errorf("must not re-stop an already-suspended app, stops = %d", dc.stops)
	}
}

func TestWake_StampsLastTrafficOnSuccess(t *testing.T) {
	st := newFakeStore()
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: true}
	w := NewWaker(st, &fakeCompose{}, &fakeProxy{}, nil)

	if err := w.Wake(context.Background(), "a1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, ok := st.lastSet["a1"]; !ok {
		t.Errorf("wake should stamp last_traffic_at so a queued suspend backs off")
	}
}

func TestWake_StartsClearsSyncsAndGatesHealth(t *testing.T) {
	st := newFakeStore()
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: true}
	st.services = []store.Service{{ServiceName: "web"}}
	dc := &fakeCompose{}
	px := &fakeProxy{}
	w := NewWaker(st, dc, px, nil)

	if err := w.Wake(context.Background(), "a1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if dc.starts != 1 {
		t.Errorf("starts = %d, want 1", dc.starts)
	}
	if st.suspendSet["a1"] {
		t.Errorf("suspended not cleared")
	}
	if px.syncs != 1 {
		t.Errorf("syncs = %d, want 1", px.syncs)
	}
	if st.svcStatus["web"] != serviceStatusRunning {
		t.Errorf("service status = %q, want running", st.svcStatus["web"])
	}
}

func TestWake_AlreadyAwakeIsNoop(t *testing.T) {
	st := newFakeStore()
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: false}
	dc := &fakeCompose{}
	px := &fakeProxy{}
	w := NewWaker(st, dc, px, nil)

	if err := w.Wake(context.Background(), "a1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if dc.starts != 0 {
		t.Errorf("an already-awake app must not be started, starts = %d", dc.starts)
	}
}

func TestWake_HealthFailureLeavesUnsuspended(t *testing.T) {
	st := newFakeStore()
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: true}
	dc := &fakeCompose{}
	px := &fakeProxy{healthErr: errors.New("unhealthy")}
	w := NewWaker(st, dc, px, nil)

	if err := w.Wake(context.Background(), "a1"); err == nil {
		t.Fatalf("Wake: expected error on health failure")
	}
	// suspended was cleared before Sync (so real routes are pushed); a failed
	// health gate leaves the stack started + un-suspended, matching the pipeline.
	if st.suspendSet["a1"] {
		t.Errorf("suspended should be cleared before Sync even on health failure")
	}
	if px.syncs != 1 {
		t.Errorf("real routes should have been pushed before the health gate")
	}
}

func TestWake_ConcurrentTriggersOneStart(t *testing.T) {
	st := newFakeStore()
	st.apps["a1"] = store.App{ID: "a1", Slug: "blog", Suspended: true}
	dc := &fakeCompose{startGate: make(chan struct{}), started: make(chan struct{})}
	px := &fakeProxy{}
	w := NewWaker(st, dc, px, nil)

	// Goroutine A wins the guard and blocks inside Start.
	done := make(chan error, 1)
	go func() { done <- w.Wake(context.Background(), "a1") }()
	<-dc.started // A has entered Start and holds the in-flight slot

	// B loses the race and must get ErrWaking without a second start.
	if err := w.Wake(context.Background(), "a1"); !errors.Is(err, ErrWaking) {
		t.Fatalf("concurrent Wake = %v, want ErrWaking", err)
	}

	close(dc.startGate) // let A finish
	if err := <-done; err != nil {
		t.Fatalf("Wake A: %v", err)
	}
	if dc.starts != 1 {
		t.Errorf("starts = %d, want exactly 1", dc.starts)
	}
}
