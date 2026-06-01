package proxy

import (
	"context"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestGenAliasAndDial(t *testing.T) {
	if got := genAlias("blog", "web", "abc1234"); got != "blog--web--abc1234" {
		t.Errorf("genAlias = %q", got)
	}
	m := newManagerWith(&fakeStore{}, newFakeCaddy(), newFakeNet())
	svc := store.Service{ServiceName: "web", InternalPort: intp(3000)}
	if got := m.genDial("blog", svc, "abc1234"); got != "blog--web--abc1234:3000" {
		t.Errorf("genDial = %q", got)
	}
}

func TestDial_HonoursRouteAlias(t *testing.T) {
	m := newManagerWith(&fakeStore{}, newFakeCaddy(), newFakeNet())
	// No override → bare alias.
	bare := store.Service{ServiceName: "web", InternalPort: intp(3000)}
	if got := m.dial("blog", bare); got != "blog--web:3000" {
		t.Errorf("dial without override = %q", got)
	}
	// Override set (post-cutover) → dial follows the generation alias.
	rolled := store.Service{ServiceName: "web", InternalPort: intp(3000), RouteAlias: strp("blog--web--g2")}
	if got := m.dial("blog", rolled); got != "blog--web--g2:3000" {
		t.Errorf("dial with override = %q", got)
	}
}

func rollingFixture() (*fakeStore, *fakeCaddy, *fakeNet, *Manager) {
	d := store.Domain{ID: "d1", AppID: "a1", Hostname: "blog.example.com", ServiceName: "web"}
	s := &fakeStore{
		app:      store.App{ID: "a1", Slug: "blog"},
		domains:  []store.Domain{d},
		services: []store.Service{{ServiceName: "web", ContainerID: strp("old"), InternalPort: intp(3000)}},
	}
	c := newFakeCaddy()
	n := newFakeNet()
	return s, c, n, newManagerWith(s, c, n)
}

func TestAttachGeneration(t *testing.T) {
	_, _, n, m := rollingFixture()
	if err := m.AttachGeneration(context.Background(), "blog", "web", "g2", "new-cid"); err != nil {
		t.Fatal(err)
	}
	if n.connected["new-cid"] != "blog--web--g2" {
		t.Errorf("new container attached as %q, want blog--web--g2", n.connected["new-cid"])
	}
}

func TestGateGeneration_BothUpstreamsThenHealthy(t *testing.T) {
	_, c, _, m := rollingFixture()
	// New generation upstream healthy in Caddy's pool.
	c.upstreams = []caddy.UpstreamStatus{
		{Address: "blog--web:3000", Fails: 0},     // old
		{Address: "blog--web--g2:3000", Fails: 0}, // new
	}
	if err := m.GateGeneration(context.Background(), "a1", "web", "g2"); err != nil {
		t.Fatalf("GateGeneration: %v", err)
	}
	// During gating the route must carry BOTH upstreams, old listed first.
	r := c.put["vac-route-d1"]
	dials := upstreamDials(r)
	if len(dials) != 2 || dials[0] != "blog--web:3000" || dials[1] != "blog--web--g2:3000" {
		t.Errorf("gate upstreams = %v, want [old new]", dials)
	}
}

func TestGateGeneration_UnhealthyNewBlocksCutover(t *testing.T) {
	_, c, _, m := rollingFixture()
	// Old healthy, new missing/failing → gate must fail (caller won't cut over).
	c.upstreams = []caddy.UpstreamStatus{{Address: "blog--web:3000", Fails: 0}}
	if err := m.GateGeneration(context.Background(), "a1", "web", "g2"); err == nil {
		t.Error("expected GateGeneration to fail when new generation never healthy")
	}
}

func TestCutover_NarrowsToNewUpstream(t *testing.T) {
	_, c, _, m := rollingFixture()
	if err := m.Cutover(context.Background(), "a1", "web", "g2"); err != nil {
		t.Fatalf("Cutover: %v", err)
	}
	r := c.put["vac-route-d1"]
	dials := upstreamDials(r)
	if len(dials) != 1 || dials[0] != "blog--web--g2:3000" {
		t.Errorf("cutover upstreams = %v, want [blog--web--g2:3000]", dials)
	}
}

func TestDetachContainer(t *testing.T) {
	_, _, n, m := rollingFixture()
	if err := m.DetachContainer(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	if len(n.disconnected) != 1 || n.disconnected[0] != "old" {
		t.Errorf("disconnected = %v, want [old]", n.disconnected)
	}
}

func upstreamDials(r caddy.Route) []string {
	if len(r.Handle) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Handle[0].Upstreams))
	for _, u := range r.Handle[0].Upstreams {
		out = append(out, u.Dial)
	}
	return out
}
