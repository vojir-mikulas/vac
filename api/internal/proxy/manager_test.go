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
	app        store.App
	domains    []store.Domain
	services   []store.Service
	created    []store.Domain
	certStatus map[string]string
}

func (f *fakeStore) GetApp(_ context.Context, _ string) (store.App, error) { return f.app, nil }
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
func (f *fakeStore) CreateDomain(_ context.Context, appID, svc, host, typ string) (store.Domain, error) {
	d := store.Domain{ID: "new-" + host, AppID: appID, ServiceName: svc, Hostname: host, Type: typ}
	f.created = append(f.created, d)
	f.domains = append(f.domains, d)
	return d, nil
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
func (f *fakeStore) SetCertStatus(_ context.Context, id, status string) error {
	if f.certStatus == nil {
		f.certStatus = map[string]string{}
	}
	f.certStatus[id] = status
	return nil
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
func (c *fakeCaddy) Ping(_ context.Context) error { return nil }

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

func TestAssignAutoDomains(t *testing.T) {
	s := &fakeStore{
		app: store.App{ID: "a1", Slug: "blog"},
		services: []store.Service{
			{ServiceName: "web", InternalPort: intp(3000)},
			{ServiceName: "worker"}, // no internal port → no domain
		},
	}
	m := newManagerWith(s, newFakeCaddy(), newFakeNet())
	if err := m.AssignAutoDomains(context.Background(), "a1"); err != nil {
		t.Fatalf("AssignAutoDomains: %v", err)
	}
	if len(s.created) != 1 || s.created[0].Hostname != "blog.vac.example.com" {
		t.Fatalf("created = %+v, want one blog.vac.example.com", s.created)
	}
	if s.created[0].Type != store.DomainTypeAuto {
		t.Errorf("type = %q, want auto", s.created[0].Type)
	}

	// Idempotent: a second call creates nothing new.
	before := len(s.created)
	_ = m.AssignAutoDomains(context.Background(), "a1")
	if len(s.created) != before {
		t.Errorf("AssignAutoDomains not idempotent: created grew to %d", len(s.created))
	}
}

func TestAssignAutoDomains_MultiService(t *testing.T) {
	s := &fakeStore{
		app: store.App{ID: "a1", Slug: "shop"},
		services: []store.Service{
			{ServiceName: "web", InternalPort: intp(3000)},
			{ServiceName: "api", InternalPort: intp(4000)},
		},
	}
	m := newManagerWith(s, newFakeCaddy(), newFakeNet())
	if err := m.AssignAutoDomains(context.Background(), "a1"); err != nil {
		t.Fatalf("AssignAutoDomains: %v", err)
	}
	got := map[string]bool{}
	for _, d := range s.created {
		got[d.Hostname] = true
	}
	if !got["web.shop.vac.example.com"] || !got["api.shop.vac.example.com"] {
		t.Errorf("multi-service hostnames wrong: %+v", got)
	}
}
