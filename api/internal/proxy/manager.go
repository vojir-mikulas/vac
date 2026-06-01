package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Store is the slice of *store.Store the manager reads and writes.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	ListDomainsByApp(ctx context.Context, appID string) ([]store.Domain, error)
	ListAllDomains(ctx context.Context) ([]store.Domain, error)
	GetDomainByHostname(ctx context.Context, hostname string) (store.Domain, error)
	CreateDomain(ctx context.Context, appID, serviceName, hostname, typ string) (store.Domain, error)
	GetService(ctx context.Context, appID, name string) (store.Service, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
	SetCertStatus(ctx context.Context, id, status string) error
}

// CaddyClient is the slice of *caddy.Client the manager drives.
type CaddyClient interface {
	PutRoute(ctx context.Context, id string, r caddy.Route) error
	DeleteRoute(ctx context.Context, id string) error
	GetRoutes(ctx context.Context) ([]caddy.Route, error)
	Upstreams(ctx context.Context) ([]caddy.UpstreamStatus, error)
	Ping(ctx context.Context) error
	Load(ctx context.Context, cfg *caddy.Config) error
}

// NetworkController is the slice of *dockercli.Compose used for vac-edge.
type NetworkController interface {
	NetworkCreate(ctx context.Context, name string) error
	NetworkConnect(ctx context.Context, network, container, alias string) error
	NetworkDisconnect(ctx context.Context, network, container string) error
}

// Config carries the proxy-layer settings.
type Config struct {
	EdgeNetwork    string        // vac-edge network name
	BaseDomain     string        // for automatic subdomains; empty disables them
	ControlDomain  string        // hostname the dashboard is served on; empty disables the route
	ControlPort    int           // port vac-api listens on inside the compose network
	HealthInterval time.Duration // Caddy active health-check interval
	HealthTimeout  time.Duration // per-check timeout + overall WaitHealthy budget
	HealthRetries  int           // WaitHealthy poll count
}

// Manager projects VAC domains into Caddy routes and manages vac-edge
// attachments. It is constructed once at startup.
type Manager struct {
	store      Store
	caddy      CaddyClient
	net        NetworkController
	cfg        Config
	logger     *slog.Logger
	baseConfig *caddy.Config // re-pushed to self-heal a Caddy restart; nil disables

	mu                 sync.RWMutex
	baseDomainOverride string // runtime override from instance_settings; "" = use cfg.BaseDomain
	hasOverride        bool
}

// SetBaseDomain installs a runtime base-domain override (from the DB-backed
// instance settings), used for automatic subdomains in place of the config
// value. Safe for concurrent use; call after a settings change or at boot.
func (m *Manager) SetBaseDomain(domain string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baseDomainOverride = domain
	m.hasOverride = true
}

// baseDomain returns the effective base domain: the runtime override when set,
// otherwise the startup config value.
func (m *Manager) baseDomain() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.hasOverride {
		return m.baseDomainOverride
	}
	return m.cfg.BaseDomain
}

// New wires a Manager.
func New(s Store, c CaddyClient, net NetworkController, cfg Config, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.EdgeNetwork == "" {
		cfg.EdgeNetwork = "vac-edge"
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = 5 * time.Second
	}
	if cfg.HealthTimeout <= 0 {
		cfg.HealthTimeout = 30 * time.Second
	}
	if cfg.HealthRetries <= 0 {
		cfg.HealthRetries = 5
	}
	return &Manager{store: s, caddy: c, net: net, cfg: cfg, logger: logger}
}

// SetBaseConfig records the base Caddy config so the manager can self-heal a
// proxy restart (see ensureBaseConfig). Called once at startup; safe to leave
// unset, which disables the self-heal probe.
func (m *Manager) SetBaseConfig(cfg *caddy.Config) { m.baseConfig = cfg }

// EnsureNetwork creates vac-edge if it doesn't already exist.
func (m *Manager) EnsureNetwork(ctx context.Context) error {
	return m.net.NetworkCreate(ctx, m.cfg.EdgeNetwork)
}

// isMissingServer reports whether a Caddy error indicates VAC's base server
// tree is absent (Caddy reverted to the admin-only bootstrap config after a
// restart). Such errors carry "invalid traversal path" for config/apps/http.
func isMissingServer(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid traversal path")
}

// ensureBaseConfig makes sure Caddy still has VAC's base server tree. Caddy
// loses it on restart (it boots from the admin-only Caddyfile), after which
// every route push fails with "invalid traversal path". A cheap GetRoutes
// probe detects the missing tree; we re-POST the base config to restore it.
// Best-effort and idempotent — a probe error that is NOT a missing-server
// signal (e.g. proxy unreachable) is left for the caller's own push to surface.
func (m *Manager) ensureBaseConfig(ctx context.Context) {
	if m.baseConfig == nil {
		return
	}
	if _, err := m.caddy.GetRoutes(ctx); err == nil || !isMissingServer(err) {
		return
	}
	if err := m.caddy.Load(ctx, m.baseConfig); err != nil {
		m.logger.Warn("proxy: reload base config failed", "err", err)
		return
	}
	m.logger.Info("proxy: reloaded base config (caddy had reverted to bootstrap)")
}

// Ping checks the Caddy admin API is reachable — backs the /health soft probe.
func (m *Manager) Ping(ctx context.Context) error {
	return m.caddy.Ping(ctx)
}

// routeFor builds the Caddy route for one domain. The upstream dials the
// service's vac-edge alias on its container port; an active health check lets
// Caddy (and, via the upstreams endpoint, WaitHealthy) track liveness.
func (m *Manager) routeFor(d store.Domain, svc store.Service, slug string) caddy.Route {
	return m.routeForDials(d, healthPathOf(svc), m.dial(slug, svc))
}

// routeForDials builds a reverse-proxy route for one domain pointing at one or
// more upstream dial addresses, all under a single active health check. The
// multi-dial form backs the A3 generation gate, where the route briefly carries
// both the old and new generation upstreams so Caddy health-checks the new one
// while the old keeps serving; the single-dial form is the steady state.
func (m *Manager) routeForDials(d store.Domain, healthPath string, dials ...string) caddy.Route {
	path := "/"
	if healthPath != "" {
		path = healthPath
	}
	ups := make([]caddy.Upstream, 0, len(dials))
	for _, dl := range dials {
		ups = append(ups, caddy.Upstream{Dial: dl})
	}
	return caddy.Route{
		ID:    routeID(d.ID),
		Match: []caddy.Match{{Host: []string{d.Hostname}}},
		Handle: []caddy.Handler{{
			Handler:   "reverse_proxy",
			Upstreams: ups,
			HealthChecks: &caddy.HealthChecks{Active: &caddy.ActiveHealthCheck{
				Path:     path,
				Interval: m.cfg.HealthInterval.String(),
				Timeout:  m.cfg.HealthTimeout.String(),
			}},
		}},
	}
}

// healthPathOf is the active-health-check path for a service: the operator-set
// path, or "/" when unset.
func healthPathOf(svc store.Service) string {
	if svc.HealthPath != nil && *svc.HealthPath != "" {
		return *svc.HealthPath
	}
	return "/"
}

func (m *Manager) dial(slug string, svc store.Service) string {
	return fmt.Sprintf("%s:%d", routeAliasFor(slug, svc), portOr(svc.InternalPort))
}

// routeAliasFor is the vac-edge alias Caddy should dial for a service: the
// live route_alias override (set during a zero-downtime cutover to point at the
// new generation), or the stable bare {slug}--{service} alias when unset. Used
// by dial — so routeFor and WaitHealthy follow the live generation too.
func routeAliasFor(slug string, svc store.Service) string {
	if svc.RouteAlias != nil && *svc.RouteAlias != "" {
		return *svc.RouteAlias
	}
	return alias(slug, svc.ServiceName)
}

func portOr(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

// Sync pushes the desired routes for one app (attaching its HTTP containers to
// vac-edge) and prunes any Caddy routes no longer backed by a domain row.
func (m *Manager) Sync(ctx context.Context, appID string) error {
	if err := m.EnsureNetwork(ctx); err != nil {
		m.logger.Warn("proxy: ensure network", "err", err)
	}
	m.ensureBaseConfig(ctx)
	errApply := m.applyApp(ctx, appID)
	errControl := m.applyControlRoute(ctx)
	errPrune := m.pruneOrphans(ctx)
	return errors.Join(errApply, errControl, errPrune)
}

// applyApp attaches the app's routable containers to vac-edge and pushes a
// route per domain. Domains whose service has no container / internal port yet
// have their route removed (nothing to route to).
func (m *Manager) applyApp(ctx context.Context, appID string) error {
	app, err := m.store.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	domains, err := m.store.ListDomainsByApp(ctx, appID)
	if err != nil {
		return err
	}
	services, err := m.store.ListServicesForApp(ctx, appID)
	if err != nil {
		return err
	}
	byName := make(map[string]store.Service, len(services))
	for _, s := range services {
		byName[s.ServiceName] = s
	}

	attached := make(map[string]bool)
	var errs []error
	for _, d := range domains {
		svc, ok := byName[d.ServiceName]
		routable := ok && svc.ContainerID != nil && *svc.ContainerID != "" && svc.InternalPort != nil
		if !routable {
			// Not deployed yet (or portless) — make sure no stale route lingers.
			if err := m.caddy.DeleteRoute(ctx, routeID(d.ID)); err != nil {
				m.logger.Debug("proxy: delete stale route", "domain", d.Hostname, "err", err)
			}
			continue
		}
		if !attached[*svc.ContainerID] {
			if err := m.net.NetworkConnect(ctx, m.cfg.EdgeNetwork, *svc.ContainerID, alias(app.Slug, svc.ServiceName)); err != nil {
				errs = append(errs, fmt.Errorf("attach %s: %w", svc.ServiceName, err))
				continue
			}
			attached[*svc.ContainerID] = true
		}
		if err := m.caddy.PutRoute(ctx, routeID(d.ID), m.routeFor(d, svc, app.Slug)); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", d.Hostname, err))
			_ = m.store.SetCertStatus(ctx, d.ID, store.CertStatusError)
		}
	}
	return errors.Join(errs...)
}

// pruneOrphans deletes any vac-route-* route in Caddy not backed by a current
// domain row. Handles routes orphaned by a crash between DB delete and Caddy
// delete, and by domain deletion (the delete handler calls Sync afterwards).
func (m *Manager) pruneOrphans(ctx context.Context) error {
	domains, err := m.store.ListAllDomains(ctx)
	if err != nil {
		return err
	}
	valid := make(map[string]bool, len(domains))
	for _, d := range domains {
		valid[routeID(d.ID)] = true
	}
	routes, err := m.caddy.GetRoutes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range routes {
		if strings.HasPrefix(r.ID, routeIDPrefix) && !valid[r.ID] {
			if err := m.caddy.DeleteRoute(ctx, r.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// applyControlRoute pushes (or removes) the dashboard's Caddy route. With a
// ControlDomain set the dashboard is served HTTPS on it; with no domain we
// remove any stale route so the operator can hit the host-published port
// without Caddy intercepting the hostname later.
func (m *Manager) applyControlRoute(ctx context.Context) error {
	if m.cfg.ControlDomain == "" {
		if err := m.caddy.DeleteRoute(ctx, controlRouteID); err != nil {
			m.logger.Debug("proxy: delete stale control route", "err", err)
		}
		return nil
	}
	port := m.cfg.ControlPort
	if port <= 0 {
		port = 3000
	}
	route := caddy.Route{
		ID:    controlRouteID,
		Match: []caddy.Match{{Host: []string{m.cfg.ControlDomain}}},
		Handle: []caddy.Handler{{
			Handler:   "reverse_proxy",
			Upstreams: []caddy.Upstream{{Dial: fmt.Sprintf("vac-api:%d", port)}},
			HealthChecks: &caddy.HealthChecks{Active: &caddy.ActiveHealthCheck{
				Path:     "/health",
				Interval: m.cfg.HealthInterval.String(),
				Timeout:  m.cfg.HealthTimeout.String(),
			}},
		}},
	}
	return m.caddy.PutRoute(ctx, controlRouteID, route)
}

// IsControlDomain reports whether host is the configured control-plane
// hostname. Used by the on-demand TLS ask hook to allow Caddy to issue a cert
// for the dashboard without a matching domain row in the DB.
func (m *Manager) IsControlDomain(host string) bool {
	return m.cfg.ControlDomain != "" && strings.EqualFold(host, m.cfg.ControlDomain)
}

// Teardown removes an app's live routes and detaches its containers from
// vac-edge, leaving the domain rows intact. Used on a temporary stop so a
// stopped app returns a clean 502/503 instead of proxying to a dead upstream.
func (m *Manager) Teardown(ctx context.Context, appID string) error {
	domains, err := m.store.ListDomainsByApp(ctx, appID)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range domains {
		if err := m.caddy.DeleteRoute(ctx, routeID(d.ID)); err != nil {
			m.logger.Debug("proxy: teardown route", "domain", d.Hostname, "err", err)
		}
	}
	services, err := m.store.ListServicesForApp(ctx, appID)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, s := range services {
		if s.ContainerID != nil && *s.ContainerID != "" {
			if err := m.net.NetworkDisconnect(ctx, m.cfg.EdgeNetwork, *s.ContainerID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Reconcile rebuilds the entire route set and re-attaches live containers from
// the DB on boot, then prunes orphans. Idempotent.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.EnsureNetwork(ctx); err != nil {
		m.logger.Warn("proxy: reconcile ensure network", "err", err)
	}
	m.ensureBaseConfig(ctx)
	domains, err := m.store.ListAllDomains(ctx)
	if err != nil {
		return err
	}
	apps := make(map[string]bool)
	for _, d := range domains {
		apps[d.AppID] = true
	}
	var errs []error
	for appID := range apps {
		if err := m.applyApp(ctx, appID); err != nil {
			errs = append(errs, err)
		}
	}
	if err := m.applyControlRoute(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := m.pruneOrphans(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		m.logger.Warn("proxy: reconcile completed with errors", "err", err)
		return err
	}
	m.logger.Info("proxy: reconcile complete", "apps", len(apps), "domains", len(domains))
	return nil
}

// AssignAutoDomains creates an `auto` domain for each HTTP-exposing service of
// an app that doesn't already have one, when a base domain is configured.
// Idempotent — an existing hostname is skipped.
func (m *Manager) AssignAutoDomains(ctx context.Context, appID string) error {
	baseDomain := m.baseDomain()
	if baseDomain == "" {
		return nil
	}
	app, err := m.store.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	services, err := m.store.ListServicesForApp(ctx, appID)
	if err != nil {
		return err
	}
	var httpServices []store.Service
	for _, s := range services {
		if s.InternalPort != nil {
			httpServices = append(httpServices, s)
		}
	}
	multi := len(httpServices) > 1

	var errs []error
	for _, s := range httpServices {
		host := AutoSubdomain(app.Slug, s.ServiceName, baseDomain, multi)
		if host == "" {
			continue
		}
		if _, err := m.store.GetDomainByHostname(ctx, host); err == nil {
			continue // already assigned
		} else if !errors.Is(err, store.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		if _, err := m.store.CreateDomain(ctx, appID, s.ServiceName, host, store.DomainTypeAuto); err != nil && !errors.Is(err, store.ErrConflict) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
