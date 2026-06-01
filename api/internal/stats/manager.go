package stats

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// StatSource polls container stats and resolves container start times.
// *dockercli.Compose satisfies it.
type StatSource interface {
	Stats(ctx context.Context, ids []string) ([]dockercli.StatSample, error)
	ContainerStartedAt(ctx context.Context, id string) (time.Time, error)
}

// StatStore is the slice of *store.Store the collector reads.
type StatStore interface {
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
	// ListRunningServices enumerates every live container across all apps for
	// the scrape-time SnapshotAll (Prometheus exposition, plan 13).
	ListRunningServices(ctx context.Context) ([]store.RunningServiceRef, error)
}

// AppSample is a one-shot per-service resource reading for the Prometheus
// exposition. Unlike the live WS Sample, it carries the app slug for labelling.
type AppSample struct {
	App        string
	Service    string
	CPUPercent float64
	MemBytes   int64
}

// Publisher tees frames to the hub. *ws.Hub satisfies it.
type Publisher interface {
	Publish(topic string, msg []byte)
}

// Manager runs per-app stats collectors and a host ticker on demand. Collectors
// start when their topic gains its first subscriber (via the hub's OnSubscribe
// hook) and stop when the last one leaves — no `docker stats` runs while nobody
// is watching.
type Manager struct {
	docker   StatSource
	store    StatStore
	hub      Publisher
	host     *HostCollector
	interval time.Duration
	logger   *slog.Logger

	mu         sync.Mutex
	parentCtx  context.Context
	collectors map[string]context.CancelFunc // appID -> cancel
	hostCancel context.CancelFunc
	uptime     map[string]time.Time // containerID -> startedAt cache
}

// NewManager wires the manager. interval defaults to 2s.
func NewManager(docker StatSource, st StatStore, hub Publisher, host *HostCollector, interval time.Duration, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Manager{
		docker:     docker,
		store:      st,
		hub:        hub,
		host:       host,
		interval:   interval,
		logger:     logger,
		collectors: make(map[string]context.CancelFunc),
		uptime:     make(map[string]time.Time),
	}
}

// Start records the lifetime context that gates all collectors. Call before the
// server starts handling requests so subscribe hooks have a context to derive
// from. Collectors and the host ticker stop when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.parentCtx = ctx
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.stopAll()
	}()
}

// OnSubscribe starts the collector for a stats/host topic on first subscriber.
func (m *Manager) OnSubscribe(topic string) {
	if topic == ws.HostTopic {
		m.startHost()
		return
	}
	if appID, ok := ws.ParseStatsTopic(topic); ok {
		m.startApp(appID)
	}
}

// OnUnsubscribe stops the collector for a topic on last subscriber.
func (m *Manager) OnUnsubscribe(topic string) {
	if topic == ws.HostTopic {
		m.stopHost()
		return
	}
	if appID, ok := ws.ParseStatsTopic(topic); ok {
		m.stopApp(appID)
	}
}

// Snapshot returns a one-off host snapshot for the REST endpoint.
func (m *Manager) Snapshot(ctx context.Context) HostSnapshot {
	if m.host == nil {
		return HostSnapshot{}
	}
	return m.host.Snapshot(ctx)
}

// SnapshotAll polls `docker stats` once for every running container and returns
// a per-service sample for the Prometheus exposition. It is independent of the
// subscriber-gated live collectors — a point-in-time read for one scrape. On any
// error it returns nil so a scrape degrades to "no per-app samples" rather than
// failing the whole endpoint.
func (m *Manager) SnapshotAll(ctx context.Context) []AppSample {
	refs, err := m.store.ListRunningServices(ctx)
	if err != nil || len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	byID := make(map[string]store.RunningServiceRef, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ContainerID)
		byID[r.ContainerID] = r
	}
	samples, err := m.docker.Stats(ctx, ids)
	if err != nil {
		m.logger.Debug("stats: snapshot-all poll failed", "err", err)
		return nil
	}
	out := make([]AppSample, 0, len(samples))
	for _, s := range samples {
		ref, ok := matchRef(s.ID, byID)
		if !ok {
			continue
		}
		memUsed, _ := parsePair(s.MemUsage)
		out = append(out, AppSample{
			App:        ref.Slug,
			Service:    ref.ServiceName,
			CPUPercent: parsePercent(s.CPUPerc),
			MemBytes:   memUsed,
		})
	}
	return out
}

func (m *Manager) startApp(appID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.parentCtx == nil || m.collectors[appID] != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.parentCtx) //nolint:gosec // cancel is retained in m.collectors and invoked by stopApp/stopAll
	m.collectors[appID] = cancel
	go m.runApp(ctx, appID)
}

func (m *Manager) stopApp(appID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.collectors[appID]; cancel != nil {
		cancel()
		delete(m.collectors, appID)
	}
}

func (m *Manager) startHost() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.parentCtx == nil || m.hostCancel != nil || m.host == nil {
		return
	}
	ctx, cancel := context.WithCancel(m.parentCtx)
	m.hostCancel = cancel
	go m.runHost(ctx)
}

func (m *Manager) stopHost() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostCancel != nil {
		m.hostCancel()
		m.hostCancel = nil
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, cancel := range m.collectors {
		cancel()
		delete(m.collectors, id)
	}
	if m.hostCancel != nil {
		m.hostCancel()
		m.hostCancel = nil
	}
}

func (m *Manager) runApp(ctx context.Context, appID string) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	m.collectApp(ctx, appID) // immediate first sample
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.collectApp(ctx, appID)
		}
	}
}

func (m *Manager) collectApp(ctx context.Context, appID string) {
	services, err := m.store.ListServicesForApp(ctx, appID)
	if err != nil {
		return
	}
	idToService := make(map[string]string)
	var ids []string
	for _, svc := range services {
		if svc.ContainerID == nil || *svc.ContainerID == "" {
			continue
		}
		if svc.Status != deploy.ServiceStatusRunning && svc.Status != deploy.ServiceStatusDegraded {
			continue
		}
		idToService[*svc.ContainerID] = svc.ServiceName
		ids = append(ids, *svc.ContainerID)
	}
	if len(ids) == 0 {
		return
	}
	samples, err := m.docker.Stats(ctx, ids)
	if err != nil {
		m.logger.Debug("stats: poll failed", "app", appID, "err", err)
		return
	}
	now := time.Now()
	for _, s := range samples {
		fullID, service := matchService(s.ID, idToService)
		if service == "" {
			continue
		}
		rx, tx := parsePair(s.NetIO)
		memUsed, _ := parsePair(s.MemUsage)
		sample := Sample{
			CPUPercent:    parsePercent(s.CPUPerc),
			MemBytes:      memUsed,
			MemPercent:    parsePercent(s.MemPerc),
			NetRxBytes:    rx,
			NetTxBytes:    tx,
			UptimeSeconds: m.uptimeSeconds(ctx, fullID, now),
		}
		frame, ferr := ws.Marshal(ws.TypeStats, service, now, sample)
		if ferr != nil {
			continue
		}
		m.hub.Publish(ws.StatsTopic(appID), frame)
	}
}

// uptimeSeconds returns the container's age, caching its start time (which never
// changes for a given container id).
func (m *Manager) uptimeSeconds(ctx context.Context, containerID string, now time.Time) int64 {
	m.mu.Lock()
	started, ok := m.uptime[containerID]
	m.mu.Unlock()
	if !ok {
		t, err := m.docker.ContainerStartedAt(ctx, containerID)
		if err != nil {
			return 0
		}
		started = t
		m.mu.Lock()
		m.uptime[containerID] = started
		m.mu.Unlock()
	}
	if started.IsZero() {
		return 0
	}
	d := now.Sub(started).Seconds()
	if d < 0 {
		return 0
	}
	return int64(d)
}

func (m *Manager) runHost(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := m.host.Snapshot(ctx)
			frame, err := ws.Marshal(ws.TypeHost, "", time.Now(), snap)
			if err != nil {
				continue
			}
			m.hub.Publish(ws.HostTopic, frame)
		}
	}
}

// matchService resolves a `docker stats` short id back to the full container id
// (and its service) the store knows. docker stats echoes a 12-char id; we match
// by prefix in either direction.
func matchService(sampleID string, idToService map[string]string) (fullID, service string) {
	for id, svc := range idToService {
		if id == sampleID || strings.HasPrefix(id, sampleID) || strings.HasPrefix(sampleID, id) {
			return id, svc
		}
	}
	return "", ""
}

// matchRef is matchService's counterpart for SnapshotAll: it resolves a
// `docker stats` short id back to the running-service ref the store knows.
func matchRef(sampleID string, byID map[string]store.RunningServiceRef) (store.RunningServiceRef, bool) {
	for id, ref := range byID {
		if id == sampleID || strings.HasPrefix(id, sampleID) || strings.HasPrefix(sampleID, id) {
			return ref, true
		}
	}
	return store.RunningServiceRef{}, false
}
