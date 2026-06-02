package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ---- fakes ----

type fakeStore struct {
	app      store.App
	domains  []store.Domain
	services []store.Service
}

func (f *fakeStore) GetApp(_ context.Context, _ string) (store.App, error) { return f.app, nil }
func (f *fakeStore) ListApps(_ context.Context) ([]store.App, error)       { return []store.App{f.app}, nil }
func (f *fakeStore) ListDomainsByApp(_ context.Context, _ string) ([]store.Domain, error) {
	return f.domains, nil
}
func (f *fakeStore) ListAllDomains(_ context.Context) ([]store.Domain, error) { return f.domains, nil }
func (f *fakeStore) GetDomainByHostname(_ context.Context, host string) (store.Domain, error) {
	for _, d := range f.domains {
		if d.Hostname == host {
			return d, nil
		}
	}
	return store.Domain{}, store.ErrNotFound
}

func (f *fakeStore) GetService(_ context.Context, _, name string) (store.Service, error) {
	for _, s := range f.services {
		if s.ServiceName == name {
			return s, nil
		}
	}
	return store.Service{}, store.ErrNotFound
}

func (f *fakeStore) ListServicesForApp(_ context.Context, _ string) ([]store.Service, error) {
	return f.services, nil
}

type fakeCaddy struct {
	put       map[string]caddy.Route
	deleted   []string
	existing  []caddy.Route
	upstreams []caddy.UpstreamStatus
}

func newFakeCaddy() *fakeCaddy { return &fakeCaddy{put: map[string]caddy.Route{}} }

func (c *fakeCaddy) PutRoute(_ context.Context, id string, r caddy.Route) error {
	r.ID = id
	c.put[id] = r
	return nil
}

func (c *fakeCaddy) DeleteRoute(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)
	return nil
}

func (c *fakeCaddy) GetRoutes(_ context.Context) ([]caddy.Route, error) {
	out := append([]caddy.Route{}, c.existing...)
	for _, r := range c.put {
		out = append(out, r)
	}
	return out, nil
}

func (c *fakeCaddy) Upstreams(_ context.Context) ([]caddy.UpstreamStatus, error) {
	return c.upstreams, nil
}
func (c *fakeCaddy) Ping(_ context.Context) error                  { return nil }
func (c *fakeCaddy) Load(_ context.Context, _ *caddy.Config) error { return nil }

type fakeNet struct {
	connected    map[string]string // container -> alias
	disconnected []string
	created      []string
}

func newFakeNet() *fakeNet { return &fakeNet{connected: map[string]string{}} }

func (n *fakeNet) NetworkCreate(_ context.Context, name string) error {
	n.created = append(n.created, name)
	return nil
}

func (n *fakeNet) NetworkConnect(_ context.Context, _, container, alias string) error {
	n.connected[container] = alias
	return nil
}

func (n *fakeNet) NetworkDisconnect(_ context.Context, _, container string) error {
	n.disconnected = append(n.disconnected, container)
	return nil
}

// ---- helpers ----

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

func newManagerWith(s Store, c CaddyClient, n NetworkController) *Manager {
	return New(s, c, n, Config{EdgeNetwork: "vac-edge", BaseDomain: "vac.example.com", HealthRetries: 1, HealthTimeout: time.Second}, nil)
}

// ---- tests ----

func TestRouteFor(t *testing.T) {
	m := newManagerWith(&fakeStore{}, newFakeCaddy(), newFakeNet())
	d := store.Domain{ID: "d1", Hostname: "blog.vac.example.com", ServiceName: "web"}
	svc := store.Service{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}

	r := m.routeFor(d, svc, "blog")
	if r.ID != "vac-route-d1" {
		t.Errorf("route id = %q", r.ID)
	}
	if len(r.Match) != 1 || len(r.Match[0].Host) != 1 || r.Match[0].Host[0] != "blog.vac.example.com" {
		t.Errorf("host matcher = %+v", r.Match)
	}
	if len(r.Handle) != 1 || r.Handle[0].Handler != "reverse_proxy" {
		t.Fatalf("handler = %+v", r.Handle)
	}
	if got := r.Handle[0].Upstreams[0].Dial; got != "blog--web:3000" {
		t.Errorf("dial = %q, want blog--web:3000", got)
	}
	if ac := r.Handle[0].HealthChecks.Active; ac == nil || ac.Path != "/" {
		t.Errorf("active health check = %+v", r.Handle[0].HealthChecks)
	}
}

func TestRouteFor_CustomHealthPath(t *testing.T) {
	m := newManagerWith(&fakeStore{}, newFakeCaddy(), newFakeNet())
	d := store.Domain{ID: "d1", Hostname: "h", ServiceName: "web"}
	svc := store.Service{ServiceName: "web", InternalPort: intp(8080), HealthPath: strp("/healthz")}
	r := m.routeFor(d, svc, "app")
	if r.Handle[0].HealthChecks.Active.Path != "/healthz" {
		t.Errorf("health path = %q", r.Handle[0].HealthChecks.Active.Path)
	}
}

func TestSync_AttachesAndRoutes(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "blog.vac.example.com", ServiceName: "web"}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	n := newFakeNet()
	m := newManagerWith(s, c, n)

	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n.connected["c1"] != "blog--web" {
		t.Errorf("container not attached with alias: %+v", n.connected)
	}
	if _, ok := c.put["vac-route-d1"]; !ok {
		t.Errorf("route not pushed: %+v", c.put)
	}
}

func TestSync_PrunesOrphanRoutes(t *testing.T) {
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  nil, // no domains → every vac-route-* is orphan
		services: nil,
	}
	c := newFakeCaddy()
	c.existing = []caddy.Route{{ID: "vac-route-stale"}, {ID: "user-route-keep"}}
	m := newManagerWith(s, c, newFakeNet())

	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	var sawStale, sawKeep bool
	for _, id := range c.deleted {
		if id == "vac-route-stale" {
			sawStale = true
		}
		if id == "user-route-keep" {
			sawKeep = true
		}
	}
	if !sawStale {
		t.Errorf("orphan vac-route-stale not pruned; deleted=%v", c.deleted)
	}
	if sawKeep {
		t.Errorf("non-VAC route was deleted; deleted=%v", c.deleted)
	}
}

func TestWaitHealthy(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "h", ServiceName: "web"}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	m := newManagerWith(s, c, newFakeNet())

	// Unhealthy: upstream missing.
	if err := m.WaitHealthy(context.Background(), "a1"); err == nil {
		t.Errorf("expected unhealthy error when upstream absent")
	}

	// Healthy: upstream present with zero fails.
	c.upstreams = []caddy.UpstreamStatus{{Address: "blog--web:3000", Fails: 0}}
	if err := m.WaitHealthy(context.Background(), "a1"); err != nil {
		t.Errorf("expected healthy, got %v", err)
	}

	// Failing upstream stays unhealthy.
	c.upstreams = []caddy.UpstreamStatus{{Address: "blog--web:3000", Fails: 3}}
	if err := m.WaitHealthy(context.Background(), "a1"); err == nil {
		t.Errorf("expected unhealthy when fails > 0")
	}
}

func TestSync_DerivesAutoRoute(t *testing.T) {
	// No custom domains, just an HTTP service: Sync should derive and push the
	// auto-subdomain route (plan 09 F1) with an @id keyed on app+service.
	s := &fakeStore{
		app: store.App{ID: "a1", Slug: "blog"},
		services: []store.Service{
			{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)},
			{ServiceName: "worker"}, // no internal port → no auto host
		},
	}
	c := newFakeCaddy()
	n := newFakeNet()
	m := newManagerWith(s, c, n)
	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	r, ok := c.put["vac-auto-a1-web"]
	if !ok {
		t.Fatalf("derived auto route not pushed: %+v", c.put)
	}
	if r.Match[0].Host[0] != "blog.vac.example.com" {
		t.Errorf("auto host = %q, want blog.vac.example.com", r.Match[0].Host[0])
	}
	if n.connected["c1"] != "blog--web" {
		t.Errorf("container not attached: %+v", n.connected)
	}
	// The non-HTTP service must not get a route.
	for id := range c.put {
		if id == "vac-auto-a1-worker" {
			t.Errorf("worker (no port) should not get an auto route")
		}
	}
}

func TestSync_RedirectDomainEmits308(t *testing.T) {
	// A domain with RedirectTo set emits a 308 redirect route (no upstream),
	// independent of whether the app's container is up (plan 09 Phase 3).
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "www.example.com", ServiceName: "web", RedirectTo: "example.com"}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web"}}, // no container — redirect still serves
	}
	c := newFakeCaddy()
	m := New(s, c, newFakeNet(), Config{EdgeNetwork: "vac-edge", HealthRetries: 1, HealthTimeout: time.Second}, nil)
	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	r, ok := c.put["vac-route-d1"]
	if !ok {
		t.Fatalf("redirect route not pushed: %+v", c.put)
	}
	h := r.Handle[0]
	if h.Handler != "static_response" || h.StatusCode != 308 {
		t.Fatalf("not a 308 static_response: %+v", h)
	}
	loc := h.Headers["Location"]
	if len(loc) != 1 || loc[0] != "https://example.com{http.request.uri}" {
		t.Errorf("Location = %v, want https://example.com{http.request.uri}", loc)
	}
}

func TestIsAutoHost(t *testing.T) {
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		services: []store.Service{{ServiceName: "web", InternalPort: intp(3000)}},
	}
	m := newManagerWith(s, newFakeCaddy(), newFakeNet())
	if ok, err := m.IsAutoHost(context.Background(), "blog.vac.example.com"); err != nil || !ok {
		t.Errorf("IsAutoHost(derived) = %v, %v; want true", ok, err)
	}
	if ok, _ := m.IsAutoHost(context.Background(), "nope.vac.example.com"); ok {
		t.Errorf("IsAutoHost(unknown) = true")
	}
}

func TestSync_PrunesStaleAutoRouteAfterBaseChange(t *testing.T) {
	// An auto route under the OLD base domain must be pruned once the base
	// changes — no orphan left behind (plan 09 F1/F2).
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	// A leftover route from a previous base domain, keyed the same way but no
	// longer in the derived set because... actually the @id is app+service, so a
	// base change keeps the same @id and PutRoute overwrites it. Simulate an
	// orphan from a *removed* service instead.
	c.existing = []caddy.Route{{ID: "vac-auto-a1-gone"}}
	m := newManagerWith(s, c, newFakeNet())
	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	var pruned bool
	for _, id := range c.deleted {
		if id == "vac-auto-a1-gone" {
			pruned = true
		}
	}
	if !pruned {
		t.Errorf("stale auto route not pruned; deleted=%v", c.deleted)
	}
}

func TestApplyControlRoute_PushedWhenSet(t *testing.T) {
	s := &fakeStore{app: store.App{ID: "a1", Slug: "blog"}}
	c := newFakeCaddy()
	m := New(s, c, newFakeNet(), Config{
		EdgeNetwork:    "vac-edge",
		ControlDomain:  "vac.example.com",
		ControlPort:    3000,
		HealthInterval: time.Second,
		HealthTimeout:  time.Second,
		HealthRetries:  1,
	}, nil)

	if err := m.applyControlRoute(context.Background()); err != nil {
		t.Fatalf("applyControlRoute: %v", err)
	}
	r, ok := c.put[controlRouteID]
	if !ok {
		t.Fatalf("control route not pushed: %+v", c.put)
	}
	if len(r.Match) != 1 || len(r.Match[0].Host) != 1 || r.Match[0].Host[0] != "vac.example.com" {
		t.Errorf("control route host = %+v", r.Match)
	}
	if len(r.Handle) != 1 || r.Handle[0].Upstreams[0].Dial != "vac-api:3000" {
		t.Errorf("control route dial = %+v", r.Handle)
	}
	if !m.IsControlDomain("VAC.example.com") {
		t.Error("IsControlDomain should match case-insensitively")
	}
	if m.IsControlDomain("other.example.com") {
		t.Error("IsControlDomain matched a non-control host")
	}
}

func TestApplyControlRoute_NoopWhenEmpty(t *testing.T) {
	s := &fakeStore{app: store.App{ID: "a1", Slug: "blog"}}
	c := newFakeCaddy()
	m := New(s, c, newFakeNet(), Config{EdgeNetwork: "vac-edge"}, nil)

	if err := m.applyControlRoute(context.Background()); err != nil {
		t.Fatalf("applyControlRoute: %v", err)
	}
	if _, ok := c.put[controlRouteID]; ok {
		t.Errorf("control route should not be pushed when ControlDomain is empty")
	}
	// And the manager should report no host as the control domain.
	if m.IsControlDomain("vac.example.com") {
		t.Error("IsControlDomain returned true with empty ControlDomain")
	}
}

func TestAutoHosts_MultiService(t *testing.T) {
	s := &fakeStore{
		app: store.App{ID: "a1", Slug: "shop"},
		services: []store.Service{
			{ServiceName: "web", InternalPort: intp(3000)},
			{ServiceName: "api", InternalPort: intp(4000)},
		},
	}
	m := newManagerWith(s, newFakeCaddy(), newFakeNet())
	hosts, err := m.AutoHosts(context.Background())
	if err != nil {
		t.Fatalf("AutoHosts: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hosts {
		got[h.Hostname] = true
	}
	if !got["web.shop.vac.example.com"] || !got["api.shop.vac.example.com"] {
		t.Errorf("multi-service hostnames wrong: %+v", got)
	}
}
