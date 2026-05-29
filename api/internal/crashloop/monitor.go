// Package crashloop watches the Docker daemon's event stream for repeated
// container deaths within a sliding window and intervenes when a service
// crosses a configurable threshold — stopping it, marking its row
// `crash-loop`, and capturing the event for the UI.
package crashloop

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const composeProjectPrefix = "vac-"

// EventSource provides a stream of container events. dockercli.Compose
// satisfies it.
type EventSource interface {
	Events(ctx context.Context) (<-chan dockercli.Event, error)
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
	AppendRuntimeLogs(ctx context.Context, appID string, rows []store.RuntimeLogRow) error
}

// Config tunes the monitor. Both fields come from VAC env vars.
type Config struct {
	Threshold int           // restarts within Window before tripping
	Window    time.Duration // sliding window length
}

// Monitor is the long-running supervisor goroutine.
type Monitor struct {
	src    EventSource
	stop   ServiceStopper
	store  MonitorStore
	cfg    Config
	logger *slog.Logger

	mu      sync.Mutex
	windows map[string]*window // key: project+"/"+service
	tripped map[string]bool    // services already in crash-loop; avoid re-firing
}

// New returns a Monitor wired with production deps.
func New(src EventSource, stop ServiceStopper, s MonitorStore, cfg Config, logger *slog.Logger) *Monitor {
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
		src:     src,
		stop:    stop,
		store:   s,
		cfg:     cfg,
		logger:  logger,
		windows: make(map[string]*window),
		tripped: make(map[string]bool),
	}
}

// Run subscribes to the event stream and processes die events until ctx is
// cancelled. If the underlying stream errors, Run retries on a backoff so
// a daemon restart doesn't permanently disable monitoring.
func (m *Monitor) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if err := m.runOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			m.logger.Warn("crashloop: events stream error; retrying", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		// Clean exit (channel closed) — reset backoff and try again.
		backoff = time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (m *Monitor) runOnce(ctx context.Context) error {
	ch, err := m.src.Events(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			m.handle(ctx, ev)
		}
	}
}

// handle is the per-event entry point. Public so tests can drive the
// monitor synchronously without spinning up the goroutine machinery.
func (m *Monitor) Handle(ctx context.Context, ev dockercli.Event) { m.handle(ctx, ev) }

func (m *Monitor) handle(ctx context.Context, ev dockercli.Event) {
	if ev.Action != "die" {
		return
	}
	project := ev.ComposeProject()
	service := ev.ComposeService()
	if !strings.HasPrefix(project, composeProjectPrefix) || service == "" {
		return
	}
	key := project + "/" + service
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
	_ = m.store.AppendRuntimeLogs(ctx, app.ID, []store.RuntimeLogRow{
		{ServiceName: service, Stream: store.RuntimeLogStreamSystem, Message: msg},
	})
	logger.Info("crashloop: tripped", "count", count, "window", m.cfg.Window)
}

// Reset clears the crash-loop flag for a service so the next sequence of
// deaths can re-trip. Called by the lifecycle restart handler once the
// user explicitly recovers a stopped service.
func (m *Monitor) Reset(projectName, service string) {
	key := projectName + "/" + service
	m.mu.Lock()
	delete(m.tripped, key)
	delete(m.windows, key)
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
