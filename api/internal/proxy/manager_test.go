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

func (f *fakeStore) SetAppMaintenanceActive(_ context.Context, _ string, active bool) error {
	f.app.MaintenanceActive = active
	return nil
}

func (f *fakeStore) ClearAppMaintenanceActiveIfManualOff(_ context.Context, _ string) (bool, error) {
	if f.app.MaintenanceMode || !f.app.MaintenanceActive {
		return false, nil
	}
	f.app.MaintenanceActive = false
	return true, nil
}

type fakeCaddy struct {
	put       map[string]caddy.Route
	deleted   []string
	existing  []caddy.Route
	upstreams []caddy.UpstreamStatus
	certSets  [][]caddy.CertKeyPair
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
func (c *fakeCaddy) PutCertSet(_ context.Context, certs []caddy.CertKeyPair) error {
	c.certSets = append(c.certSets, certs)
	return nil
}

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

	r := m.routeFor(d, svc, "blog", nil)
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
	r := m.routeFor(d, svc, "app", nil)
	if r.Handle[0].HealthChecks.Active.Path != "/healthz" {
		t.Errorf("health path = %q", r.Handle[0].HealthChecks.Active.Path)
	}
}

func TestRouteFor_RateLimit(t *testing.T) {
	m := newManagerWith(&fakeStore{}, newFakeCaddy(), newFakeNet())
	d := store.Domain{ID: "d1", Hostname: "h", ServiceName: "web"}
	svc := store.Service{ServiceName: "web", InternalPort: intp(8080)}

	// No limit → a single reverse_proxy handler.
	if r := m.routeFor(d, svc, "app", nil); len(r.Handle) != 1 || r.Handle[0].Handler != "reverse_proxy" {
		t.Fatalf("no-limit handlers = %+v", r.Handle)
	}
	if r := m.routeFor(d, svc, "app", intp(0)); len(r.Handle) != 1 {
		t.Errorf("zero rpm should not add a rate_limit handler: %+v", r.Handle)
	}

	// A positive limit prepends rate_limit before reverse_proxy.
	r := m.routeFor(d, svc, "app", intp(120))
	if len(r.Handle) != 2 || r.Handle[0].Handler != "rate_limit" || r.Handle[1].Handler != "reverse_proxy" {
		t.Fatalf("rate-limited chain = %+v", r.Handle)
	}
	zone, ok := r.Handle[0].RateLimits["vac-route-d1"]
	if !ok || zone.MaxEvents != 120 || zone.Window != "1m" {
		t.Errorf("rate_limit zone = %+v (ok=%v)", zone, ok)
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

// ---- bring-your-own cert push (dns-automation plan B) ----

type fakeCertSource struct{ certs []store.UploadedCert }

func (f fakeCertSource) ListUploadedCerts(context.Context) ([]store.UploadedCert, error) {
	return f.certs, nil
}

// fakeKeyOpener returns the sealed bytes verbatim — a deterministic stand-in for
// the crypto.Box so the test asserts on plaintext.
type fakeKeyOpener struct{}

func (fakeKeyOpener) Open(b []byte) ([]byte, error) { return b, nil }

func TestSyncCertsPushesUploaded(t *testing.T) {
	fc := newFakeCaddy()
	m := newManagerWith(&fakeStore{}, fc, newFakeNet())
	m.SetCertSource(fakeCertSource{certs: []store.UploadedCert{
		{DomainID: "d1", Hostname: "x.example.com", CertPEM: "CERTPEM", KeyEnc: []byte("KEYPEM")},
	}}, fakeKeyOpener{})

	if err := m.SyncCerts(context.Background()); err != nil {
		t.Fatalf("SyncCerts: %v", err)
	}
	if len(fc.certSets) != 1 {
		t.Fatalf("expected one cert push, got %d", len(fc.certSets))
	}
	got := fc.certSets[0]
	if len(got) != 1 {
		t.Fatalf("expected one cert, got %d", len(got))
	}
	if got[0].Certificate != "CERTPEM" || got[0].Key != "KEYPEM" {
		t.Errorf("cert/key = %q / %q", got[0].Certificate, got[0].Key)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "vac-cert-d1" {
		t.Errorf("tags = %v, want [vac-cert-d1]", got[0].Tags)
	}
}

func TestSyncCertsNoSourceIsNoop(t *testing.T) {
	fc := newFakeCaddy()
	m := newManagerWith(&fakeStore{}, fc, newFakeNet())
	if err := m.SyncCerts(context.Background()); err != nil {
		t.Fatalf("SyncCerts: %v", err)
	}
	if len(fc.certSets) != 0 {
		t.Errorf("expected no cert push when BYO certs unwired, got %d", len(fc.certSets))
	}
}

func TestClearedCertPushesEmptySet(t *testing.T) {
	fc := newFakeCaddy()
	m := newManagerWith(&fakeStore{}, fc, newFakeNet())
	m.SetCertSource(fakeCertSource{certs: nil}, fakeKeyOpener{})
	if err := m.SyncCerts(context.Background()); err != nil {
		t.Fatalf("SyncCerts: %v", err)
	}
	if len(fc.certSets) != 1 || len(fc.certSets[0]) != 0 {
		t.Errorf("expected one empty cert push, got %v", fc.certSets)
	}
}

// TestSync_MaintenanceMode verifies that when an app's effective maintenance
// flag is set, Sync swaps every host's route to a 503 static_response carrying
// the rendered page under the SAME @id (a clean swap), and that turning it off
// restores the proxy route in place.
func TestSync_MaintenanceMode(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "blog.vac.example.com", ServiceName: "web"}
	custom := "<h1>down for maintenance</h1>"
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog", MaintenanceActive: true, MaintenanceHTML: &custom},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	m := newManagerWith(s, c, newFakeNet())

	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	r, ok := c.put["vac-route-d1"]
	if !ok {
		t.Fatalf("maintenance route not pushed under the host @id: %+v", c.put)
	}
	if len(r.Handle) != 1 || r.Handle[0].Handler != "static_response" {
		t.Fatalf("handler = %+v, want static_response", r.Handle)
	}
	if r.Handle[0].StatusCode != 503 {
		t.Errorf("status = %d, want 503", r.Handle[0].StatusCode)
	}
	if r.Handle[0].Body != custom {
		t.Errorf("body = %q, want the custom page", r.Handle[0].Body)
	}

	// Turning maintenance off restores the proxy route in place (same @id).
	s.app.MaintenanceActive = false
	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync (off): %v", err)
	}
	r = c.put["vac-route-d1"]
	if len(r.Handle) != 1 || r.Handle[0].Handler != "reverse_proxy" {
		t.Fatalf("after off, handler = %+v, want reverse_proxy", r.Handle)
	}
}

// TestSync_MaintenanceMode_DefaultPage verifies the built-in default page is
// served when the app has no custom HTML.
func TestSync_MaintenanceMode_DefaultPage(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "h", ServiceName: "web"}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog", MaintenanceActive: true},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	m := newManagerWith(s, c, newFakeNet())
	if err := m.Sync(context.Background(), "a1"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	r := c.put["vac-route-d1"]
	if r.Handle[0].Body == "" {
		t.Errorf("expected the default page body to be served")
	}
}

// ---- scale-to-zero wake routes ----

func TestInstallWakeRoutes_SwapsRoutesAndDetaches(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "blog.vac.example.com", ServiceName: "web"}
	svc := store.Service{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  []store.Domain{d},
		services: []store.Service{svc},
	}
	c := newFakeCaddy()
	n := newFakeNet()
	m := New(s, c, n, Config{EdgeNetwork: "vac-edge", BaseDomain: "vac.example.com", ControlPort: 9393, WakeToken: "sek"}, nil)

	if err := m.InstallWakeRoutes(context.Background(), "a1"); err != nil {
		t.Fatalf("InstallWakeRoutes: %v", err)
	}

	// Custom-domain route is swapped for a wake route at the same @id.
	r, ok := c.put["vac-route-d1"]
	if !ok {
		t.Fatalf("wake route not pushed: %+v", c.put)
	}
	if len(r.Handle) != 3 {
		t.Fatalf("wake route handlers = %+v", r.Handle)
	}
	if r.Handle[0].Handler != "headers" || r.Handle[0].Request == nil {
		t.Errorf("handler[0] = %+v, want headers+request", r.Handle[0])
	}
	if got := r.Handle[0].Request.Set["X-Caddy-Ask-Token"]; len(got) != 1 || got[0] != "sek" {
		t.Errorf("wake token header = %+v", got)
	}
	if r.Handle[1].Handler != "rewrite" || r.Handle[1].URI != "/__vac_wake" {
		t.Errorf("handler[1] = %+v, want rewrite /__vac_wake", r.Handle[1])
	}
	if got := r.Handle[2].Upstreams[0].Dial; got != "vac-api:9393" {
		t.Errorf("wake upstream = %q, want vac-api:9393", got)
	}

	// Container is detached from vac-edge.
	if len(n.disconnected) != 1 || n.disconnected[0] != "c1" {
		t.Errorf("disconnected = %+v, want [c1]", n.disconnected)
	}
}

func TestApplyApp_SuspendedInstallsWakeRoutes(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "blog.vac.example.com", ServiceName: "web"}
	svc := store.Service{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog", Suspended: true},
		domains:  []store.Domain{d},
		services: []store.Service{svc},
	}
	c := newFakeCaddy()
	n := newFakeNet()
	m := newManagerWith(s, c, n)

	// Reconcile drives applyApp; a suspended app must get wake routes (not proxy
	// routes) and must NOT be (re)attached to vac-edge.
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	r, ok := c.put["vac-route-d1"]
	if !ok {
		t.Fatalf("route not pushed: %+v", c.put)
	}
	if got := r.Handle[len(r.Handle)-1].Upstreams[0].Dial; got != "vac-api:9393" {
		t.Errorf("suspended route should dial vac-api, got %q", got)
	}
	if len(n.connected) != 0 {
		t.Errorf("suspended app should not attach to edge, got %+v", n.connected)
	}
}

// TestMaintainOnOff verifies the deploy pipeline's auto-maintenance seam:
// MaintainOn raises the 503 page on every host; MaintainOff restores the proxy
// route when manual maintenance is off, and preserves the page when it's on.
func TestMaintainOnOff(t *testing.T) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "h", ServiceName: "web"}
	mk := func() (*fakeStore, *fakeCaddy, *Manager) {
		s := &fakeStore{
			app:      store.App{ID: "a1", Slug: "blog"},
			domains:  []store.Domain{d},
			services: []store.Service{{ServiceName: "web", ContainerID: strp("c1"), InternalPort: intp(3000)}},
		}
		c := newFakeCaddy()
		return s, c, newManagerWith(s, c, newFakeNet())
	}

	// On → 503 page pushed under the host @id.
	s, c, m := mk()
	if err := m.MaintainOn(context.Background(), "a1"); err != nil {
		t.Fatalf("MaintainOn: %v", err)
	}
	if !s.app.MaintenanceActive {
		t.Fatal("MaintainOn should set the effective flag")
	}
	if got := c.put["vac-route-d1"].Handle[0].Handler; got != "static_response" {
		t.Fatalf("after MaintainOn, handler = %q, want static_response", got)
	}

	// Off with manual maintenance NOT set → flag cleared, proxy route restored.
	if err := m.MaintainOff(context.Background(), "a1"); err != nil {
		t.Fatalf("MaintainOff: %v", err)
	}
	if s.app.MaintenanceActive {
		t.Fatal("MaintainOff should clear the flag when manual mode is off")
	}
	if got := c.put["vac-route-d1"].Handle[0].Handler; got != "reverse_proxy" {
		t.Fatalf("after MaintainOff, handler = %q, want reverse_proxy", got)
	}

	// Off with manual maintenance set → page survives the deploy.
	s, c, m = mk()
	s.app.MaintenanceMode = true
	if err := m.MaintainOn(context.Background(), "a1"); err != nil {
		t.Fatalf("MaintainOn: %v", err)
	}
	if err := m.MaintainOff(context.Background(), "a1"); err != nil {
		t.Fatalf("MaintainOff: %v", err)
	}
	if !s.app.MaintenanceActive {
		t.Fatal("manual maintenance must survive a deploy's MaintainOff")
	}
	if got := c.put["vac-route-d1"].Handle[0].Handler; got != "static_response" {
		t.Fatalf("manual maintenance page should remain, handler = %q", got)
	}
}
