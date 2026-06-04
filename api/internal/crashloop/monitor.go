// Package crashloop watches the Docker daemon's event stream for repeated
// container deaths within a sliding window and intervenes when a service
// crosses a configurable threshold — stopping it, marking its row
// `crash-loop`, and capturing the event for the UI.
package crashloop

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// oomExitCode is the exit status of a container killed by the OOM killer
// (128 + SIGKILL/9). A `die` with this code is the trigger to confirm an OOM via
// docker inspect; other codes are never OOMs.
const oomExitCode = 137

const composeProjectPrefix = "vac-"

// EventSubscriber yields a stream of container events from a shared bus.
// *dockerevents.Bus satisfies it. nil disables the Run loop (tests drive
// Handle directly).
type EventSubscriber interface {
	Subscribe() (<-chan dockercli.Event, func())
}

// ServiceStopper stops a single service in a compose project. The crash-loop
// monitor uses this to halt a runaway container without taking the whole
// stack down.
type ServiceStopper interface {
	Stop(ctx context.Context, projectName, service string) error
}

// MonitorStore is the slice of *store.Store the monitor reads and writes.
type MonitorStore interface {
	GetAppBySlug(ctx context.Context, slug string) (store.App, error)
	UpdateServiceStatus(ctx context.Context, appID, name, status string, exitCode *int) error
	IncrementServiceRestart(ctx context.Context, appID, name string) (int, error)
	IncrementServiceOOM(ctx context.Context, appID, name string, exitCode *int) (int, error)
	AppendRuntimeLogs(ctx context.Context, appID string, rows []store.RuntimeLogRow) ([]int64, error)
}

// Inspector confirms whether a dead container was OOM-killed. The docker event
// stream omits this flag, so we inspect on a suspicious (exit 137) death.
// *dockercli.Compose satisfies it; nil disables OOM detection.
type Inspector interface {
	ContainerOOMKilled(ctx context.Context, containerID string) (bool, error)
}

// Notifier fires a notification when a service trips a guard. Implemented by
// notify.Dispatcher; nil disables notifications.
type Notifier interface {
	CrashLoop(appName, appID, service string, restarts int, exitCode *int)
	OOMKilled(appName, appID, service string, limitMB int)
}

// Config tunes the monitor. Both fields come from VAC env vars.
type Config struct {
	Threshold int           // restarts within Window before tripping
	Window    time.Duration // sliding window length
}

// Monitor is the long-running supervisor goroutine.
type Monitor struct {
	src       EventSubscriber
	stop      ServiceStopper
	store     MonitorStore
	notifier  Notifier
	inspector Inspector
	cfg       Config
	logger    *slog.Logger

	mu          sync.Mutex
	windows     map[string]*window // key: project+"/"+service
	tripped     map[string]bool    // services already in crash-loop; avoid re-firing
	oomNotified map[string]bool    // services with an OOM already notified this episode
}

// New returns a Monitor wired with production deps.
func New(src EventSubscriber, stop ServiceStopper, s MonitorStore, cfg Config, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.Window <= 0 {
		cfg.Window = 2 * time.Minute
	}
	return &Monitor{
		src:         src,
		stop:        stop,
		store:       s,
		cfg:         cfg,
		logger:      logger,
		windows:     make(map[string]*window),
		tripped:     make(map[string]bool),
		oomNotified: make(map[string]bool),
	}
}

// Run subscribes to the shared event bus and processes die events until ctx is
// cancelled. Reconnection across daemon restarts is owned by the bus, so the
// subscription channel is stable for the lifetime of this loop.
func (m *Monitor) Run(ctx context.Context) {
	if m.src == nil {
		return
	}
	ch, cancel := m.src.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			m.handle(ctx, ev)
		}
	}
}

// Handle is the per-event entry point. Public so tests can drive the
// monitor synchronously without spinning up the goroutine machinery.
func (m *Monitor) Handle(ctx context.Context, ev dockercli.Event) { m.handle(ctx, ev) }

func (m *Monitor) handle(ctx context.Context, ev dockercli.Event) {
	project := ev.ComposeProject()
	service := ev.ComposeService()
	if !strings.HasPrefix(project, composeProjectPrefix) || service == "" {
		return
	}
	key := project + "/" + service

	// A container's removal ends its episode: drop its per-service tracking so the
	// windows/tripped/oomNotified maps can't grow one never-evicted entry per
	// container ever seen (a redeploy destroys the old container and mints a new
	// id). `destroy` fires on `docker rm`/compose recreate — never between the
	// die/start of a restart loop — so clearing here can't prematurely un-trip a
	// service mid-loop; an explicit operator recover (Reset) is the other path.
	if ev.Action == "destroy" {
		m.mu.Lock()
		delete(m.windows, key)
		delete(m.tripped, key)
		delete(m.oomNotified, key)
		m.mu.Unlock()
		return
	}
	if ev.Action != "die" {
		return
	}
	// OOM detection runs first and independently of the crash-loop threshold: a
	// single OOM kill is worth surfacing even before a service trips the loop.
	m.maybeHandleOOM(ctx, project, service, ev)

	m.mu.Lock()
	if m.tripped[key] {
		m.mu.Unlock()
		return
	}
	w := m.windows[key]
	if w == nil {
		w = &window{maxAge: m.cfg.Window}
		m.windows[key] = w
	}
	w.record(ev.EventTime())
	count := w.size()
	tripped := count >= m.cfg.Threshold
	if tripped {
		m.tripped[key] = true
	}
	m.mu.Unlock()

	if !tripped {
		return
	}
	m.trip(ctx, project, service, ev, count)
}

func (m *Monitor) trip(ctx context.Context, project, service string, ev dockercli.Event, count int) {
	slug := strings.TrimPrefix(project, composeProjectPrefix)
	logger := m.logger.With("project", project, "service", service)

	app, err := m.store.GetAppBySlug(ctx, slug)
	if err != nil {
		logger.Warn("crashloop: app not found", "err", err)
		return
	}
	if err := m.stop.Stop(ctx, project, service); err != nil {
		logger.Warn("crashloop: stop failed", "err", err)
	}

	exitCode := parseExitCode(ev.Actor.Attributes["exitCode"])
	if err := m.store.UpdateServiceStatus(ctx, app.ID, service, deploy.ServiceStatusCrashLoop, exitCode); err != nil {
		logger.Warn("crashloop: status update failed", "err", err)
	}
	_, _ = m.store.IncrementServiceRestart(ctx, app.ID, service)

	msg := "crash-loop: stopped after " + strconv.Itoa(count) + " restarts in " + m.cfg.Window.String()
	if exitCode != nil {
		msg += " (last exit code " + strconv.Itoa(*exitCode) + ")"
	}
	_, _ = m.store.AppendRuntimeLogs(ctx, app.ID, []store.RuntimeLogRow{
		{ServiceName: service, Stream: store.RuntimeLogStreamSystem, Message: msg},
	})
	if m.notifier != nil {
		m.notifier.CrashLoop(app.Name, app.ID, service, count, exitCode)
	}
	logger.Info("crashloop: tripped", "count", count, "window", m.cfg.Window)
}

// maybeHandleOOM confirms and surfaces an out-of-memory kill. A `die` carries no
// OOM flag, so we only pay for a docker inspect when the exit code looks like an
// OOM (137). On confirmation we bump the service's oom_killed_count and — once
// per episode — log a system line and fire a notification. The count still
// increments on every OOM so the UI/metric reflect the true tally, but the
// notification fires once (until Reset) to avoid spamming during a restart loop.
func (m *Monitor) maybeHandleOOM(ctx context.Context, project, service string, ev dockercli.Event) {
	if m.inspector == nil {
		return
	}
	exitCode := parseExitCode(ev.Actor.Attributes["exitCode"])
	if exitCode == nil || *exitCode != oomExitCode {
		return
	}
	cid := ev.Actor.ID
	if cid == "" {
		cid = ev.ID
	}
	if cid == "" {
		return
	}
	oom, err := m.inspector.ContainerOOMKilled(ctx, cid)
	if err != nil || !oom {
		return
	}

	key := project + "/" + service
	m.mu.Lock()
	firstThisEpisode := !m.oomNotified[key]
	m.oomNotified[key] = true
	m.mu.Unlock()

	slug := strings.TrimPrefix(project, composeProjectPrefix)
	app, err := m.store.GetAppBySlug(ctx, slug)
	if err != nil {
		m.logger.Warn("crashloop: oom app not found", "slug", slug, "err", err)
		return
	}
	count, err := m.store.IncrementServiceOOM(ctx, app.ID, service, exitCode)
	if err != nil {
		m.logger.Warn("crashloop: oom count update failed", "err", err)
	}

	if !firstThisEpisode {
		return
	}
	limit := 0
	if app.MemLimitMB != nil {
		limit = *app.MemLimitMB
	}
	msg := "out of memory: container killed (exit 137)"
	if limit > 0 {
		msg = fmt.Sprintf("out of memory: killed for exceeding its %d MiB limit", limit)
	}
	_, _ = m.store.AppendRuntimeLogs(ctx, app.ID, []store.RuntimeLogRow{
		{ServiceName: service, Stream: store.RuntimeLogStreamSystem, Message: msg},
	})
	if m.notifier != nil {
		m.notifier.OOMKilled(app.Name, app.ID, service, limit)
	}
	m.logger.Info("crashloop: oom-killed", "project", project, "service", service, "count", count, "limit_mb", limit)
}

// SetNotifier wires an optional notifier fired when a service trips. Kept off
// the constructor so existing call sites and tests are unaffected.
func (m *Monitor) SetNotifier(n Notifier) { m.notifier = n }

// SetInspector wires the docker inspector used to confirm OOM kills. Kept off
// the constructor for the same reason as SetNotifier.
func (m *Monitor) SetInspector(i Inspector) { m.inspector = i }

// Reset clears the crash-loop flag for a service so the next sequence of
// deaths can re-trip. Called by the lifecycle restart handler once the
// user explicitly recovers a stopped service.
func (m *Monitor) Reset(projectName, service string) {
	key := projectName + "/" + service
	m.mu.Lock()
	delete(m.tripped, key)
	delete(m.windows, key)
	delete(m.oomNotified, key)
	m.mu.Unlock()
}

func parseExitCode(s string) *int {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// window is a fixed-duration sliding window of timestamps. Trim drops any
// entries older than maxAge.
type window struct {
	maxAge time.Duration
	events []time.Time
}

func (w *window) record(t time.Time) {
	w.events = append(w.events, t)
	w.trim(t)
}

func (w *window) size() int { return len(w.events) }

func (w *window) trim(now time.Time) {
	cutoff := now.Add(-w.maxAge)
	idx := 0
	for ; idx < len(w.events); idx++ {
		if w.events[idx].After(cutoff) {
			break
		}
	}
	if idx > 0 {
		w.events = w.events[idx:]
	}
}
