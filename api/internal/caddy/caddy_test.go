package caddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBaseConfig(t *testing.T) {
	cfg := BaseConfig(BaseOptions{
		AccessLogPath: "/var/log/caddy/access.log",
		AskURL:        "http://vac-api:3000/internal/caddy/ask",
		ACMECA:        "https://acme-staging.example/dir",
	})

	srv := cfg.Apps.HTTP.Servers[ServerName]
	if srv == nil {
		t.Fatal("server not present")
	}
	if len(srv.Listen) != 2 || srv.Listen[0] != ":80" || srv.Listen[1] != ":443" {
		t.Errorf("listen = %v", srv.Listen)
	}
	if srv.Routes == nil {
		t.Error("routes should be a non-nil empty slice so the array path exists for appends")
	}
	if cfg.Admin == nil || cfg.Admin.Listen != ":2019" {
		t.Errorf("admin = %+v", cfg.Admin)
	}
	if cfg.Apps.TLS == nil || cfg.Apps.TLS.Automation.OnDemand == nil {
		t.Fatal("on-demand TLS not configured")
	}
	perm := cfg.Apps.TLS.Automation.OnDemand.Permission
	if perm == nil || perm.Module != "http" || perm.Endpoint != "http://vac-api:3000/internal/caddy/ask" {
		t.Errorf("permission = %+v", perm)
	}
	// ACME CA should be applied to a policy's issuer.
	if len(cfg.Apps.TLS.Automation.Policies) == 0 || len(cfg.Apps.TLS.Automation.Policies[0].Issuers) == 0 {
		t.Fatal("acme issuer not set")
	}

	// Routes must marshal as [] not null (POST append target).
	b, _ := json.Marshal(srv)
	if !strings.Contains(string(b), `"routes":[]`) {
		t.Errorf("routes did not marshal as empty array: %s", b)
	}
}

func TestRouteMarshalsID(t *testing.T) {
	r := Route{
		ID:    "vac-route-x",
		Match: []Match{{Host: []string{"h.example.com"}}},
		Handle: []Handler{{
			Handler:   "reverse_proxy",
			Upstreams: []Upstream{{Dial: "a--b:3000"}},
		}},
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"@id":"vac-route-x"`) {
		t.Errorf("@id not marshalled: %s", b)
	}
}

func TestClientRouteLifecycle(t *testing.T) {
	var (
		posted  bool
		deleted bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/id/"):
			deleted = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/config/apps/http/servers/vac/routes":
			posted = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/config/apps/http/servers/vac/routes":
			_ = json.NewEncoder(w).Encode([]Route{{ID: "vac-route-1"}})
		case r.URL.Path == "/reverse_proxy/upstreams":
			_ = json.NewEncoder(w).Encode([]UpstreamStatus{{Address: "a--b:3000", Fails: 0}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx := context.Background()

	if err := c.PutRoute(ctx, "vac-route-1", Route{}); err != nil {
		t.Fatalf("PutRoute: %v", err)
	}
	if !deleted || !posted {
		t.Errorf("PutRoute should delete-then-post: deleted=%v posted=%v", deleted, posted)
	}

	routes, err := c.GetRoutes(ctx)
	if err != nil || len(routes) != 1 || routes[0].ID != "vac-route-1" {
		t.Errorf("GetRoutes = %+v, %v", routes, err)
	}

	ups, err := c.Upstreams(ctx)
	if err != nil || len(ups) != 1 || ups[0].Address != "a--b:3000" {
		t.Errorf("Upstreams = %+v, %v", ups, err)
	}
}

func TestClientUnavailable(t *testing.T) {
	c := New("http://127.0.0.1:0") // nothing listening
	if _, err := c.GetRoutes(context.Background()); err == nil {
		t.Error("expected error against dead admin API")
	}
}
