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

// Store is the slice of *store.Store the manager reads.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	ListApps(ctx context.Context) ([]store.App, error)
	ListDomainsByApp(ctx context.Context, appID string) ([]store.Domain, error)
	ListAllDomains(ctx context.Context) ([]store.Domain, error)
	GetService(ctx context.Context, appID, name string) (store.Service, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
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

// StatusEngine receives route-push outcomes so the DNS/cert status projection
// (internal/domainstatus, plan 09 F3) can surface `error` for a host whose route
// failed to push, and clear it once the push succeeds. Optional — nil disables
// the signal. Only the manager produces `error`; DNS/cert truth never overwrites
// it (and vice-versa).
type StatusEngine interface {
	SetError(host, detail string)
	ClearError(host string)
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
	engine     StatusEngine  // route-push outcome sink; nil disables

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

// BaseDomain returns the effective base domain: the runtime override when set,
// otherwise the startup config value. Exported so the status engine and
// handlers can render the same value the router uses.
func (m *Manager) BaseDomain() string {
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

// SetStatusEngine wires the DNS/cert status projection so route-push failures
// surface as `error` on the affected host. Called once at startup; nil-safe.
func (m *Manager) SetStatusEngine(e StatusEngine) { m.engine = e }

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

// AutoHost is a derived automatic subdomain: a pure function of an app's slug,
// its HTTP services, and the current base domain — never a stored row (plan 09
// F1). The status engine and CaddyAsk enumerate these the same way reconcile
// does so a derived host gets a status and can pre-issue a cert.
type AutoHost struct {
	Hostname    string
	AppID       string
	AppSlug     string
	ServiceName string
	Service     store.Service
}

// autoHostsForApp derives the automatic hostnames for one app under the current
// base domain. Empty base domain (auto-subdomains disabled) yields none.
func (m *Manager) autoHostsForApp(app store.App, services []store.Service) []AutoHost {
	base := m.BaseDomain()
	if base == "" {
		return nil
	}
	var httpServices []store.Service
	for _, s := range services {
		if s.InternalPort != nil {
			httpServices = append(httpServices, s)
		}
	}
	multi := len(httpServices) > 1
	out := make([]AutoHost, 0, len(httpServices))
	for _, s := range httpServices {
		host := AutoSubdomain(app.Slug, s.ServiceName, base, multi)
		if host == "" {
			continue
		}
		out = append(out, AutoHost{
			Hostname:    host,
			AppID:       app.ID,
			AppSlug:     app.Slug,
			ServiceName: s.ServiceName,
			Service:     s,
		})
	}
	return out
}

// AutoHosts derives every app's automatic subdomains under the current base
// domain. Used by the orphan prune, CaddyAsk, and the status engine so they all
// agree on the derived host set without a stored row.
func (m *Manager) AutoHosts(ctx context.Context) ([]AutoHost, error) {
	apps, err := m.store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	var out []AutoHost
	for _, app := range apps {
		services, err := m.store.ListServicesForApp(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, m.autoHostsForApp(app, services)...)
	}
	return out, nil
}

// IsAutoHost reports whether host is one of the currently-derived automatic
// subdomains. Backs CaddyAsk so on-demand TLS issuance is allowed for a derived
// host that has no domain row.
func (m *Manager) IsAutoHost(ctx context.Context, host string) (bool, error) {
	host = strings.ToLower(host)
	hosts, err := m.AutoHosts(ctx)
	if err != nil {
		return false, err
	}
	for _, h := range hosts {
		if strings.EqualFold(h.Hostname, host) {
			return true, nil
		}
	}
	return false, nil
}

// routeSpec is one desired Caddy route for an app: a Caddy @id, the hostname it
// matches, and either the service it proxies to or (Phase 3) a redirect target.
type routeSpec struct {
	id         string
	hostname   string
	service    string
	redirectTo string // when set, a 308 redirect route to this host (no upstream)
}

// desiredRoutes is the full set of routes an app should have: one per assigned
// custom domain (a proxy route, or a redirect route when redirect_to is set)
// plus one per derived auto host. Unassigned custom domains (no service) emit no
// route.
func (m *Manager) desiredRoutes(app store.App, domains []store.Domain, services []store.Service) []routeSpec {
	specs := make([]routeSpec, 0, len(domains))
	for _, d := range domains {
		if !d.Assigned() {
			continue
		}
		if d.RedirectTo != "" {
			specs = append(specs, routeSpec{id: routeID(d.ID), hostname: d.Hostname, redirectTo: d.RedirectTo})
			continue
		}
		specs = append(specs, routeSpec{id: routeID(d.ID), hostname: d.Hostname, service: d.ServiceName})
	}
	for _, ah := range m.autoHostsForApp(app, services) {
		specs = append(specs, routeSpec{id: autoRouteID(app.ID, ah.ServiceName), hostname: ah.Hostname, service: ah.ServiceName})
	}
	return specs
}

// redirectRoute builds a 308 redirect from hostname to target, preserving the
// request path/query via Caddy's {http.request.uri} placeholder. It dials no
// upstream, so it serves regardless of whether any app container is up.
func (m *Manager) redirectRoute(id, hostname, target string) caddy.Route {
	return caddy.Route{
		ID:    id,
		Match: []caddy.Match{{Host: []string{hostname}}},
		Handle: []caddy.Handler{{
			Handler:    "static_response",
			StatusCode: 308,
			Headers:    map[string][]string{"Location": {"https://" + target + "{http.request.uri}"}},
		}},
	}
}

// route builds the Caddy route for one hostname → service. The upstream dials
// the service's vac-edge alias on its container port; an active health check
// lets Caddy (and, via the upstreams endpoint, WaitHealthy) track liveness.
func (m *Manager) route(id, hostname string, svc store.Service, slug string) caddy.Route {
	path := "/"
	if svc.HealthPath != nil && *svc.HealthPath != "" {
		path = *svc.HealthPath
	}
	return caddy.Route{
		ID:    id,
		Match: []caddy.Match{{Host: []string{hostname}}},
		Handle: []caddy.Handler{{
			Handler:   "reverse_proxy",
			Upstreams: []caddy.Upstream{{Dial: m.dial(slug, svc)}},
			HealthChecks: &caddy.HealthChecks{Active: &caddy.ActiveHealthCheck{
				Path:     path,
				Interval: m.cfg.HealthInterval.String(),
				Timeout:  m.cfg.HealthTimeout.String(),
			}},
		}},
	}
}

// routeFor builds the Caddy route for one custom domain (thin wrapper over
// route, kept for the domain-centric call sites).
func (m *Manager) routeFor(d store.Domain, svc store.Service, slug string) caddy.Route {
	return m.route(routeID(d.ID), d.Hostname, svc, slug)
}

func (m *Manager) dial(slug string, svc store.Service) string {
	return fmt.Sprintf("%s:%d", alias(slug, svc.ServiceName), portOr(svc.InternalPort))
}

func portOr(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

// Sync pushes the desired routes for one app (attaching its HTTP containers to
// vac-edge) and prunes any Caddy routes no longer backed by a domain row or a
// derived auto host.
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

// applyApp attaches the app's routable containers to vac-edge and pushes a route
// per assigned custom domain and per derived auto host. A route whose service
// has no container / internal port yet is removed (nothing to route to).
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
	for _, spec := range m.desiredRoutes(app, domains, services) {
		// A redirect domain (Phase 3) serves a 308 with no upstream — push it
		// unconditionally (it doesn't depend on a container being up).
		if spec.redirectTo != "" {
			if err := m.caddy.PutRoute(ctx, spec.id, m.redirectRoute(spec.id, spec.hostname, spec.redirectTo)); err != nil {
				errs = append(errs, fmt.Errorf("redirect %s: %w", spec.hostname, err))
				if m.engine != nil {
					m.engine.SetError(spec.hostname, err.Error())
				}
			} else if m.engine != nil {
				m.engine.ClearError(spec.hostname)
			}
			continue
		}
		svc, ok := byName[spec.service]
		routable := ok && svc.ContainerID != nil && *svc.ContainerID != "" && svc.InternalPort != nil
		if !routable {
			// Not deployed yet (or portless) — make sure no stale route lingers.
			if err := m.caddy.DeleteRoute(ctx, spec.id); err != nil {
				m.logger.Debug("proxy: delete stale route", "host", spec.hostname, "err", err)
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
		if err := m.caddy.PutRoute(ctx, spec.id, m.route(spec.id, spec.hostname, svc, app.Slug)); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", spec.hostname, err))
			if m.engine != nil {
				m.engine.SetError(spec.hostname, err.Error())
			}
		} else if m.engine != nil {
			// A successful push clears any prior push-error so it self-heals.
			m.engine.ClearError(spec.hostname)
		}
	}
	return errors.Join(errs...)
}

// pruneOrphans deletes any VAC-managed route in Caddy (vac-route-* for custom
// domains, vac-auto-* for derived auto hosts) not backed by a current domain row
// or a currently-derived auto host. Handles routes orphaned by a crash, by
// domain deletion, and by a base-domain change (old auto routes drop out of the
// derived set and are pruned here — orphans become structurally impossible).
func (m *Manager) pruneOrphans(ctx context.Context) error {
	domains, err := m.store.ListAllDomains(ctx)
	if err != nil {
		return err
	}
	valid := make(map[string]bool, len(domains))
	for _, d := range domains {
		valid[routeID(d.ID)] = true
	}
	autoHosts, err := m.AutoHosts(ctx)
	if err != nil {
		return err
	}
	for _, ah := range autoHosts {
		valid[autoRouteID(ah.AppID, ah.ServiceName)] = true
	}
	routes, err := m.caddy.GetRoutes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range routes {
		if !isManagedRouteID(r.ID) || valid[r.ID] {
			continue
		}
		if err := m.caddy.DeleteRoute(ctx, r.ID); err != nil {
			errs = append(errs, err)
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
		port = 9393 // must match config.Default().Server.Port
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

// Teardown removes an app's live routes (custom + auto) and detaches its
// containers from vac-edge, leaving the domain rows intact. Used on a temporary
// stop so a stopped app returns a clean 502/503 instead of proxying to a dead
// upstream.
func (m *Manager) Teardown(ctx context.Context, appID string) error {
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
	var errs []error
	for _, spec := range m.desiredRoutes(app, domains, services) {
		if err := m.caddy.DeleteRoute(ctx, spec.id); err != nil {
			m.logger.Debug("proxy: teardown route", "host", spec.hostname, "err", err)
		}
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
// the DB on boot, then prunes orphans. It enumerates every app (not just those
// with custom domains) so derived auto hosts are routed too. Idempotent.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.EnsureNetwork(ctx); err != nil {
		m.logger.Warn("proxy: reconcile ensure network", "err", err)
	}
	m.ensureBaseConfig(ctx)
	apps, err := m.store.ListApps(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, app := range apps {
		if err := m.applyApp(ctx, app.ID); err != nil {
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
	m.logger.Info("proxy: reconcile complete", "apps", len(apps))
	return nil
}
