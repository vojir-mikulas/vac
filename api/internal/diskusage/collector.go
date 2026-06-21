// Package diskusage samples how much disk each app's volumes consume and alerts
// when an app's soft budget or the host disk crosses a threshold.
//
// Unlike CPU/RAM (a subscriber-gated 2s WebSocket stream in stats/manager.go),
// disk usage changes slowly and is expensive to sample, so this is a timer-driven
// background collector that persists the latest sample to Postgres — mirroring the
// long-lived-goroutine shape of certcheck/security, not the WS stats manager.
//
// Named volumes are sized cheaply from one `docker system df -v`. Bind mounts need
// a `du`-style walk, which can be slow on a spinning external HDD, so it is opt-in
// (VAC_DISK_SCAN_BINDS) and bounded by a timeout; unmeasured mounts are surfaced
// as "not measured" rather than blocking the poll.
package diskusage

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Store is the persistence surface the collector needs. *store.Store satisfies it.
type Store interface {
	ListApps(ctx context.Context) ([]store.App, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
	UpsertVolumeUsage(ctx context.Context, v store.VolumeUsage) error
	DeleteVolumeUsageForAppExcept(ctx context.Context, appID string, keepMountPaths []string) error
	// SumAppMemLimits totals the per-app RAM caps for the over-commit check.
	SumAppMemLimits(ctx context.Context) (store.MemAllocation, error)
}

// Docker is the volume-introspection surface. *dockercli.Compose satisfies it.
type Docker interface {
	VolumeSizes(ctx context.Context) (map[string]int64, error)
	ContainerMounts(ctx context.Context, id string) ([]dockercli.Mount, error)
}

// Notifier fires the storage-high and RAM-over-commit alerts. *notify.Dispatcher
// satisfies it. nil disables alerting (the collector still records usage).
type Notifier interface {
	DiskUsageHigh(appName, appID, scope, detail string)
	MemOverCommitted(detail string)
}

// HostDisk reports current host disk usage (used, total bytes). Wired to the host
// stats collector; nil disables the host-level threshold check.
type HostDisk func(ctx context.Context) (used, total uint64)

// HostMemTotal reports the box's total RAM in bytes. Wired to the host stats
// collector; nil disables the RAM-over-commit check.
type HostMemTotal func(ctx context.Context) uint64

// Config tunes the collector. Zero values fall back to sane defaults.
type Config struct {
	Interval     time.Duration // poll cadence; default 5m
	AlertPercent int           // fire at this % of the limit / host disk; default 85
	ScanBinds    bool          // opt-in: walk bind mounts with a bounded du
	Cooldown     time.Duration // min gap between repeat alerts per scope; default 1h
	InitialDelay time.Duration // wait after boot before the first sample; default 1m
	BindTimeout  time.Duration // per-bind-mount walk budget; default 1m
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.AlertPercent <= 0 || c.AlertPercent > 100 {
		c.AlertPercent = 85
	}
	if c.Cooldown <= 0 {
		c.Cooldown = time.Hour
	}
	if c.InitialDelay <= 0 {
		c.InitialDelay = time.Minute
	}
	if c.BindTimeout <= 0 {
		c.BindTimeout = time.Minute
	}
	return c
}

// alertState tracks one scope's (an app id, or "host") alert so a sustained
// overflow re-notifies on the cooldown rather than every poll, and re-arms once
// usage recovers (mirrors certcheck's notified/clear-on-recovery flag).
type alertState struct {
	firing     bool
	lastNotify time.Time
}

const (
	hostScope = "host"
	memScope  = "mem"
)

// Collector is the background sampler. Construct with New and drive with Run.
type Collector struct {
	store    Store
	docker   Docker
	notifier Notifier
	hostDisk HostDisk
	hostMem  HostMemTotal
	cfg      Config
	logger   *slog.Logger
	now      func() time.Time

	// alerts is keyed by scope. The collector is single-goroutine (Run owns it),
	// so no mutex is needed.
	alerts map[string]*alertState
}

// New wires a Collector. notifier, hostDisk, and hostMem may be nil.
func New(s Store, docker Docker, notifier Notifier, hostDisk HostDisk, hostMem HostMemTotal, cfg Config, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{
		store:    s,
		docker:   docker,
		notifier: notifier,
		hostDisk: hostDisk,
		hostMem:  hostMem,
		cfg:      cfg.withDefaults(),
		logger:   logger,
		now:      time.Now,
		alerts:   map[string]*alertState{},
	}
}

// Run samples once after InitialDelay, then every Interval until ctx is cancelled.
// Ticks never overlap: a slow sample (a big bind-mount walk) drops the missed tick
// rather than running two collections at once.
func (c *Collector) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(c.cfg.InitialDelay):
	}
	c.collectOnce(ctx)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectOnce(ctx)
		}
	}
}

func (c *Collector) collectOnce(ctx context.Context) {
	// Named-volume sizes in one daemon call. A failure degrades named volumes to
	// "not measured" rather than aborting the whole sweep (bind mounts still work).
	sizes, err := c.docker.VolumeSizes(ctx)
	if err != nil {
		c.logger.Warn("diskusage: read volume sizes", "err", err)
		sizes = map[string]int64{}
	}
	apps, err := c.store.ListApps(ctx)
	if err != nil {
		c.logger.Warn("diskusage: list apps", "err", err)
		return
	}
	for _, app := range apps {
		total, measured := c.collectApp(ctx, app, sizes)
		c.evalApp(app, total, measured)
	}
	c.evalHost(ctx)
	c.evalMemCommit(ctx)
}

// collectApp samples every volume of one app, persists the rows, prunes mounts
// that disappeared, and returns the app's total measured usage (and whether any
// mount was measured at all, so a fully-unmeasured app doesn't false-alert).
func (c *Collector) collectApp(ctx context.Context, app store.App, sizes map[string]int64) (total int64, measured bool) {
	services, err := c.store.ListServicesForApp(ctx, app.ID)
	if err != nil {
		c.logger.Warn("diskusage: list services", "app", app.Slug, "err", err)
		return 0, false
	}
	seen := []string{}
	for _, svc := range services {
		if !svc.HasVolumes || svc.ContainerID == nil || *svc.ContainerID == "" {
			continue
		}
		mounts, err := c.docker.ContainerMounts(ctx, *svc.ContainerID)
		if err != nil {
			c.logger.Debug("diskusage: inspect mounts", "app", app.Slug, "service", svc.ServiceName, "err", err)
			continue
		}
		for _, m := range mounts {
			row, ok := c.sampleMount(ctx, app.ID, svc.ServiceName, m, sizes)
			if !ok {
				continue
			}
			// Mark the mount as seen BEFORE the upsert: a transient upsert error must
			// not drop it from `seen`, or the prune below would delete this mount's
			// previously-good usage row over a momentary blip (it self-heals next
			// poll anyway).
			seen = append(seen, row.MountPath)
			if err := c.store.UpsertVolumeUsage(ctx, row); err != nil {
				c.logger.Warn("diskusage: upsert usage", "app", app.Slug, "mount", m.Destination, "err", err)
				continue
			}
			if row.UsedBytes != nil {
				total += *row.UsedBytes
				measured = true
			}
		}
	}
	if err := c.store.DeleteVolumeUsageForAppExcept(ctx, app.ID, seen); err != nil {
		c.logger.Warn("diskusage: prune usage", "app", app.Slug, "err", err)
	}
	return total, measured
}

// sampleMount turns one container mount into a usage row. It returns ok=false for
// mounts that aren't persistent data (tmpfs, the docker-socket control bind).
func (c *Collector) sampleMount(ctx context.Context, appID, service string, m dockercli.Mount, sizes map[string]int64) (store.VolumeUsage, bool) {
	row := store.VolumeUsage{AppID: appID, ServiceName: service, MountPath: m.Destination}
	switch m.Type {
	case "volume":
		row.Source = "named"
		row.VolumeName = m.Name
		if size, ok := sizes[m.Name]; ok {
			row.UsedBytes = &size
		}
	case "bind":
		// The docker socket is a control-plane bind, not app data — skip it.
		if isDockerSocket(m.Source) || isDockerSocket(m.Destination) {
			return store.VolumeUsage{}, false
		}
		row.Source = "bind"
		if c.cfg.ScanBinds {
			if size, err := c.walkBind(ctx, m.Source); err != nil {
				c.logger.Debug("diskusage: walk bind", "path", m.Source, "err", err)
			} else {
				row.UsedBytes = &size
			}
		}
	default:
		return store.VolumeUsage{}, false // tmpfs, npipe, etc.
	}
	return row, true
}

// walkBind sums a bind mount's on-disk size with a bounded walk. Slow trees on a
// spinning HDD are capped by BindTimeout; a path the control plane can't reach
// (the host path isn't mounted into vac-api) surfaces as an error → "not measured".
func (c *Collector) walkBind(ctx context.Context, path string) (int64, error) {
	walkCtx, cancel := context.WithTimeout(ctx, c.cfg.BindTimeout)
	defer cancel()
	return dirSizeBytes(walkCtx, path)
}

// evalApp fires (or clears) the per-app soft-limit alert. No limit set or nothing
// measured → no alert (and re-arm any prior firing).
func (c *Collector) evalApp(app store.App, total int64, measured bool) {
	if app.DiskLimitMB == nil || *app.DiskLimitMB <= 0 || !measured {
		c.clear(app.ID)
		return
	}
	limit := int64(*app.DiskLimitMB) * 1024 * 1024
	pct := int(total * 100 / limit)
	if pct < c.cfg.AlertPercent {
		c.clear(app.ID)
		return
	}
	detail := fmt.Sprintf("%d%% of the %s disk budget (%s used)", pct, humanBytes(limit), humanBytes(total))
	c.fire(app.ID, app.Name, app.Name, app.ID, detail)
}

// evalHost fires (or clears) the host-disk alert against the global percent.
func (c *Collector) evalHost(ctx context.Context) {
	if c.hostDisk == nil {
		return
	}
	used, total := c.hostDisk(ctx)
	if total == 0 {
		return
	}
	pct := int(used * 100 / total)
	if pct < c.cfg.AlertPercent {
		c.clear(hostScope)
		return
	}
	detail := fmt.Sprintf("Host disk is %d%% full (%s of %s)", pct, humanBytes(int64(used)), humanBytes(int64(total)))
	c.fire(hostScope, "", "", "host disk", detail)
}

// evalMemCommit fires (or clears) the box RAM-over-commit alert: apps' summed
// per-app caps vs the box's total RAM. Disabled when no host-mem source is wired.
// A soft signal, debounced exactly like the disk alerts (shared cooldown).
func (c *Collector) evalMemCommit(ctx context.Context) {
	if c.hostMem == nil {
		return
	}
	totalBytes := c.hostMem(ctx)
	if totalBytes == 0 {
		return
	}
	alloc, err := c.store.SumAppMemLimits(ctx)
	if err != nil {
		c.logger.Warn("diskusage: sum mem limits", "err", err)
		return
	}
	totalMB := int64(totalBytes / (1024 * 1024))
	if alloc.AllocatedMB <= totalMB {
		c.clear(memScope)
		return
	}
	if !c.armed(memScope) {
		return
	}
	detail := fmt.Sprintf("Apps have reserved %d MiB of RAM but the box has %d MiB — over-committed by %d MiB.",
		alloc.AllocatedMB, totalMB, alloc.AllocatedMB-totalMB)
	c.logger.Warn("diskusage: ram over-committed", "detail", detail)
	if c.notifier != nil {
		c.notifier.MemOverCommitted(detail)
	}
}

// fire notifies for a scope, debounced by the cooldown. A scope not currently
// firing notifies immediately; a sustained overflow re-notifies once per cooldown.
func (c *Collector) fire(key, appName, appID, scope, detail string) {
	if !c.armed(key) {
		return
	}
	c.logger.Warn("diskusage: storage high", "scope", scope, "detail", detail)
	if c.notifier != nil {
		c.notifier.DiskUsageHigh(appName, appID, scope, detail)
	}
}

// armed reports whether a scope should notify now, recording the notification
// time. It returns false while within the cooldown of the last fire, so a
// sustained overflow re-notifies once per cooldown rather than every poll.
func (c *Collector) armed(key string) bool {
	st := c.alerts[key]
	if st == nil {
		st = &alertState{}
		c.alerts[key] = st
	}
	now := c.now()
	if st.firing && now.Sub(st.lastNotify) < c.cfg.Cooldown {
		return false
	}
	st.firing = true
	st.lastNotify = now
	return true
}

// clear re-arms a scope so it alerts afresh on the next crossing once it recovers.
func (c *Collector) clear(key string) {
	if st := c.alerts[key]; st != nil {
		st.firing = false
	}
}

// isDockerSocket reports whether a path is the docker control socket.
func isDockerSocket(p string) bool {
	return strings.HasSuffix(strings.TrimSpace(p), "docker.sock")
}

// humanBytes renders a byte count as a compact binary-unit string (e.g. "1.5 GiB").
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
