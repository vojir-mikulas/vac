package crashloop_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crashloop"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStopper struct {
	calls atomic.Int64
}

func (f *fakeStopper) Stop(_ context.Context, _, _ string) error {
	f.calls.Add(1)
	return nil
}

type fakeStore struct {
	app           store.App
	statusUpdates atomic.Int64
	lastStatus    string
	lastExitCode  *int
	logs          atomic.Int64
	oomCount      atomic.Int64
}

func (f *fakeStore) GetAppBySlug(_ context.Context, _ string) (store.App, error) {
	return f.app, nil
}

func (f *fakeStore) UpdateServiceStatus(_ context.Context, _, _ string, status string, exit *int) error {
	f.statusUpdates.Add(1)
	f.lastStatus = status
	f.lastExitCode = exit
	return nil
}

func (f *fakeStore) IncrementServiceRestart(_ context.Context, _, _ string) (int, error) {
	return 1, nil
}

func (f *fakeStore) IncrementServiceOOM(_ context.Context, _, _ string, _ *int) (int, error) {
	return int(f.oomCount.Add(1)), nil
}

func (f *fakeStore) AppendRuntimeLogs(_ context.Context, _ string, rows []store.RuntimeLogRow) ([]int64, error) {
	f.logs.Add(1)
	ids := make([]int64, len(rows))
	return ids, nil
}

type fakeInspector struct{ oom bool }

func (f fakeInspector) ContainerOOMKilled(_ context.Context, _ string) (bool, error) {
	return f.oom, nil
}

type fakeNotifier struct {
	crashLoops atomic.Int64
	ooms       atomic.Int64
	lastLimit  int
}

func (f *fakeNotifier) CrashLoop(_, _, _ string, _ int, _ *int) { f.crashLoops.Add(1) }
func (f *fakeNotifier) OOMKilled(_, _, _ string, limitMB int) {
	f.ooms.Add(1)
	f.lastLimit = limitMB
}

func TestMonitor_TripsAfterThresholdInWindow(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{
		Threshold: 3,
		Window:    1 * time.Minute,
	}, nil)

	now := time.Now()
	for i := 0; i < 3; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 137, now.Add(time.Duration(i)*time.Second)))
	}

	if stop.calls.Load() != 1 {
		t.Errorf("stop calls = %d, want 1", stop.calls.Load())
	}
	if st.lastStatus != deploy.ServiceStatusCrashLoop {
		t.Errorf("status = %q, want crash-loop", st.lastStatus)
	}
	if st.lastExitCode == nil || *st.lastExitCode != 137 {
		t.Errorf("exit code = %v, want 137", st.lastExitCode)
	}
	if st.logs.Load() != 1 {
		t.Errorf("log writes = %d, want 1", st.logs.Load())
	}
}

func TestMonitor_DoesNotRetripWhileAlreadyTripped(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{Threshold: 2, Window: time.Minute}, nil)

	now := time.Now()
	for i := 0; i < 5; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, now.Add(time.Duration(i)*time.Second)))
	}
	if stop.calls.Load() != 1 {
		t.Errorf("expected single stop call, got %d", stop.calls.Load())
	}
	// After Reset, the next sequence should be able to trip again.
	m.Reset("vac-myapp", "web")
	for i := 0; i < 2; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, now.Add(time.Duration(10+i)*time.Second)))
	}
	if stop.calls.Load() != 2 {
		t.Errorf("after reset stop calls = %d, want 2", stop.calls.Load())
	}
}

func TestMonitor_IgnoresNonComposeEvents(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{Threshold: 1, Window: time.Minute}, nil)

	// Empty project — ignored.
	m.Handle(context.Background(), dieEvent("", "web", 1, time.Now()))
	// Non-vac project — ignored.
	m.Handle(context.Background(), dieEvent("not-vac", "web", 1, time.Now()))
	// Non-die action — ignored.
	ev := dieEvent("vac-myapp", "web", 0, time.Now())
	ev.Action = "start"
	m.Handle(context.Background(), ev)

	if stop.calls.Load() != 0 {
		t.Errorf("stop called for non-crash events: %d", stop.calls.Load())
	}
}

func TestMonitor_WindowEvictsOldEvents(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{Threshold: 3, Window: time.Minute}, nil)

	// Two deaths long ago, then one death now — should not trip.
	long := time.Now().Add(-1 * time.Hour)
	m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, long))
	m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, long.Add(time.Second)))
	m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, time.Now()))
	if stop.calls.Load() != 0 {
		t.Errorf("stop fired despite events being outside window: %d", stop.calls.Load())
	}
}

func TestMonitor_OOMDetectionNotifiesOncePerEpisode(t *testing.T) {
	limit := 256
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp", Name: "myapp", MemLimitMB: &limit}}
	notif := &fakeNotifier{}
	// High threshold so the crash-loop path never trips during this test —
	// we're isolating OOM handling.
	m := crashloop.New(nil, &fakeStopper{}, st, crashloop.Config{Threshold: 99, Window: time.Minute}, nil)
	m.SetNotifier(notif)
	m.SetInspector(fakeInspector{oom: true})

	now := time.Now()
	for i := 0; i < 3; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 137, now.Add(time.Duration(i)*time.Second)))
	}

	if st.oomCount.Load() != 3 {
		t.Errorf("oom count = %d, want 3 (incremented every kill)", st.oomCount.Load())
	}
	if notif.ooms.Load() != 1 {
		t.Errorf("oom notifications = %d, want 1 (once per episode)", notif.ooms.Load())
	}
	if notif.lastLimit != 256 {
		t.Errorf("notified limit = %d, want 256", notif.lastLimit)
	}

	// After Reset the next OOM notifies again.
	m.Reset("vac-myapp", "web")
	m.Handle(context.Background(), dieEvent("vac-myapp", "web", 137, now.Add(time.Minute)))
	if notif.ooms.Load() != 2 {
		t.Errorf("after reset oom notifications = %d, want 2", notif.ooms.Load())
	}
}

func TestMonitor_NonOOMExitNotInspected(t *testing.T) {
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	notif := &fakeNotifier{}
	m := crashloop.New(nil, &fakeStopper{}, st, crashloop.Config{Threshold: 99, Window: time.Minute}, nil)
	m.SetNotifier(notif)
	m.SetInspector(fakeInspector{oom: true}) // would report OOM if asked

	// Exit code 1 (not 137) must never reach the inspector → no OOM handling.
	m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, time.Now()))
	if st.oomCount.Load() != 0 || notif.ooms.Load() != 0 {
		t.Errorf("non-137 exit triggered OOM handling: count=%d notifs=%d", st.oomCount.Load(), notif.ooms.Load())
	}
}

func TestMonitor_CleanExitDoesNotCount(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{Threshold: 3, Window: time.Minute}, nil)

	// Real docker emits exitCode="0" on a clean exit; a process that exits 0 and
	// is restarted (restart: always, or an operator restart) is not a crash and
	// must not accumulate toward the threshold.
	now := time.Now()
	for i := 0; i < 5; i++ {
		m.Handle(context.Background(), dieEventExit("vac-myapp", "web", "0", now.Add(time.Duration(i)*time.Second)))
	}
	if stop.calls.Load() != 0 {
		t.Errorf("clean (exit 0) deaths tripped the loop: stop calls = %d, want 0", stop.calls.Load())
	}
}

func TestMonitor_ResetProjectReArms(t *testing.T) {
	stop := &fakeStopper{}
	st := &fakeStore{app: store.App{ID: "app-1", Slug: "myapp"}}
	m := crashloop.New(nil, stop, st, crashloop.Config{Threshold: 2, Window: time.Minute}, nil)

	now := time.Now()
	for i := 0; i < 2; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, now.Add(time.Duration(i)*time.Second)))
	}
	if stop.calls.Load() != 1 {
		t.Fatalf("expected one trip, got %d", stop.calls.Load())
	}
	// A whole-stack operator recovery clears the project's trip state.
	m.ResetProject("vac-myapp")
	for i := 0; i < 2; i++ {
		m.Handle(context.Background(), dieEvent("vac-myapp", "web", 1, now.Add(time.Duration(10+i)*time.Second)))
	}
	if stop.calls.Load() != 2 {
		t.Errorf("after ResetProject stop calls = %d, want 2", stop.calls.Load())
	}
}

// dieEventExit builds a die event carrying an explicit exitCode attribute (the
// shared dieEvent helper omits it for code 0, which docker never does).
func dieEventExit(project, service, exitCode string, at time.Time) dockercli.Event {
	ev := dieEvent(project, service, 1, at)
	ev.Actor.Attributes["exitCode"] = exitCode
	return ev
}

func dieEvent(project, service string, exitCode int, at time.Time) dockercli.Event {
	attrs := map[string]string{
		"com.docker.compose.project": project,
		"com.docker.compose.service": service,
	}
	if exitCode != 0 {
		attrs["exitCode"] = ""
		if exitCode > 0 {
			attrs["exitCode"] = stringInt(exitCode)
		}
	}
	return dockercli.Event{
		Action:   "die",
		Type:     "container",
		ID:       "c-" + project + "-" + service,
		TimeNano: at.UnixNano(),
		Actor: dockercli.EventActor{
			ID:         "c-" + project + "-" + service,
			Attributes: attrs,
		},
	}
}

func stringInt(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	bp := len(b)
	for i > 0 {
		bp--
		b[bp] = digits[i%10]
		i /= 10
	}
	if neg {
		bp--
		b[bp] = '-'
	}
	return string(b[bp:])
}
