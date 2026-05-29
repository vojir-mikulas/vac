package logstream

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const composeProjectPrefix = "vac-"

// SupervisorStore is the slice of *store.Store the supervisor reads.
type SupervisorStore interface {
	ListApps(ctx context.Context) ([]store.App, error)
	GetAppBySlug(ctx context.Context, slug string) (store.App, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
}

// EventSubscriber yields container events from the shared bus.
type EventSubscriber interface {
	Subscribe() (<-chan dockercli.Event, func())
}

// Config tunes the supervisor and its followers.
type Config struct {
	RingBuffer    int           // newest lines kept per service (mvp ring buffer)
	FlushInterval time.Duration // batch flush cadence
	TrimInterval  time.Duration // ring-buffer trim cadence per follower
	MaxBatch      int           // rows per insert before forcing a flush
	ResyncEvery   time.Duration // full reconcile safety net
}

func (c *Config) defaults() {
	if c.RingBuffer <= 0 {
		c.RingBuffer = 10000
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 250 * time.Millisecond
	}
	if c.TrimInterval <= 0 {
		c.TrimInterval = 30 * time.Second
	}
	if c.MaxBatch <= 0 {
		c.MaxBatch = 200
	}
	if c.ResyncEvery <= 0 {
		c.ResyncEvery = 60 * time.Second
	}
}

type handle struct {
	appID   string
	service string
	cancel  context.CancelFunc
}

// Supervisor keeps one follower per running container, reconciling against the
// store on deploy/lifecycle changes, on container events, and on a periodic
// safety-net resync.
type Supervisor struct {
	src    LogSource
	sink   Sink
	store  SupervisorStore
	pub    Publisher
	events EventSubscriber
	cfg    Config
	logger *slog.Logger

	parentCtx context.Context

	mu        sync.Mutex
	followers map[string]*handle // containerID -> handle
}

// New wires a supervisor. src+sink are usually the same *dockercli.Compose /
// *store.Store; events is the shared bus (nil disables event-driven reconcile).
func New(src LogSource, sink Sink, st SupervisorStore, pub Publisher, events EventSubscriber, cfg Config, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.defaults()
	return &Supervisor{
		src:       src,
		sink:      sink,
		store:     st,
		pub:       pub,
		events:    events,
		cfg:       cfg,
		logger:    logger,
		followers: make(map[string]*handle),
	}
}

// Run owns the supervisor lifecycle: boot reconcile, event-driven reconcile,
// periodic resync, and follower teardown on ctx cancel.
func (s *Supervisor) Run(ctx context.Context) {
	s.parentCtx = ctx
	s.BootSync(ctx)

	var events <-chan dockercli.Event
	var cancel func()
	if s.events != nil {
		events, cancel = s.events.Subscribe()
		defer cancel()
	}

	resync := time.NewTicker(s.cfg.ResyncEvery)
	defer resync.Stop()

	for {
		select {
		case <-ctx.Done():
			s.shutdown()
			return
		case ev, ok := <-events:
			if !ok {
				events = nil // bus closed; rely on resync
				continue
			}
			s.onEvent(ctx, ev)
		case <-resync.C:
			s.BootSync(ctx)
		}
	}
}

// BootSync reconciles followers for every app. Safe to call repeatedly.
func (s *Supervisor) BootSync(ctx context.Context) {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		s.logger.Warn("logstream: list apps for boot sync", "err", err)
		return
	}
	for _, a := range apps {
		s.ReconcileApp(ctx, a.ID)
	}
}

func (s *Supervisor) onEvent(ctx context.Context, ev dockercli.Event) {
	project := ev.ComposeProject()
	if !strings.HasPrefix(project, composeProjectPrefix) {
		return
	}
	switch ev.Action {
	case "start", "die", "stop", "kill", "destroy", "restart":
		// fall through to reconcile
	default:
		return
	}
	slug := strings.TrimPrefix(project, composeProjectPrefix)
	app, err := s.store.GetAppBySlug(ctx, slug)
	if err != nil {
		return
	}
	s.ReconcileApp(ctx, app.ID)
}

// ReconcileApp ensures the running set of followers for one app matches the
// store: a follower is started for each running service container that lacks
// one, and followers for that app's vanished containers are cancelled. The DB
// read happens outside the lock; only the followers-map diff is locked.
func (s *Supervisor) ReconcileApp(ctx context.Context, appID string) {
	services, err := s.store.ListServicesForApp(ctx, appID)
	if err != nil {
		s.logger.Warn("logstream: list services", "app", appID, "err", err)
		return
	}
	desired := make(map[string]string) // containerID -> serviceName
	for _, svc := range services {
		if svc.ContainerID == nil || *svc.ContainerID == "" {
			continue
		}
		if !shouldFollow(svc.Status) {
			continue
		}
		desired[*svc.ContainerID] = svc.ServiceName
	}

	parent := s.parentCtx
	if parent == nil {
		parent = ctx
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Start followers for newly-desired containers.
	for cid, service := range desired {
		if _, ok := s.followers[cid]; ok {
			continue
		}
		fctx, cancel := context.WithCancel(parent)
		s.followers[cid] = &handle{appID: appID, service: service, cancel: cancel}
		f := &follower{
			src: s.src, sink: s.sink, pub: s.pub,
			appID: appID, service: service, container: cid,
			ringBuffer: s.cfg.RingBuffer,
			flushEvery: s.cfg.FlushInterval,
			trimEvery:  s.cfg.TrimInterval,
			maxBatch:   s.cfg.MaxBatch,
			logger:     s.logger,
		}
		go func(cid string) {
			f.run(fctx)
			s.forget(cid)
		}(cid)
	}
	// Cancel followers for this app whose container is no longer desired.
	for cid, h := range s.followers {
		if h.appID != appID {
			continue
		}
		if _, ok := desired[cid]; !ok {
			h.cancel()
			delete(s.followers, cid)
		}
	}
}

// forget removes a follower entry after its goroutine exits on its own (the
// container's log stream ended). Guarded so it doesn't race a concurrent cancel.
func (s *Supervisor) forget(containerID string) {
	s.mu.Lock()
	if h, ok := s.followers[containerID]; ok {
		h.cancel()
		delete(s.followers, containerID)
	}
	s.mu.Unlock()
}

func (s *Supervisor) shutdown() {
	s.mu.Lock()
	for cid, h := range s.followers {
		h.cancel()
		delete(s.followers, cid)
	}
	s.mu.Unlock()
}

// count reports the number of live followers — used by tests.
func (s *Supervisor) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.followers)
}

// shouldFollow reports whether a service in this status is expected to be
// producing logs worth capturing. Stopped / crash-looped / errored containers
// have already been captured by the follower that was live before they died.
func shouldFollow(status string) bool {
	switch status {
	case deploy.ServiceStatusRunning, deploy.ServiceStatusDegraded, deploy.ServiceStatusDeploying:
		return true
	}
	return false
}
