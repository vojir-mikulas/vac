package addon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_ListAndGet(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	list := r.List()
	if len(list) == 0 {
		t.Fatal("catalog is empty")
	}
	g, ok := r.Get("grafana")
	if !ok {
		t.Fatal("grafana template missing")
	}
	if g.Name == "" || g.ComposeFile != "compose.yaml" {
		t.Errorf("grafana manifest wrong: %+v", g)
	}
	if g.FootprintMB <= 0 {
		t.Errorf("grafana footprint should be set, got %d", g.FootprintMB)
	}
	if g.DefaultEnv["GF_ADMIN_PASSWORD"] != "@random" {
		t.Errorf("grafana should declare a @random admin password, got %q", g.DefaultEnv["GF_ADMIN_PASSWORD"])
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get returned ok for an unknown template")
	}
}

func TestRegistry_ServiceHealthPaths(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	// Grafana 302-redirects "/" → /login (not 2xx), so it must declare an
	// explicit Caddy health path or the upstream never goes healthy (503).
	paths := r.ServiceHealthPaths("grafana")
	if got := paths["grafana"]; got != "/api/health" {
		t.Errorf("grafana health path = %q, want /api/health", got)
	}
	if r.ServiceHealthPaths("nope") != nil {
		t.Error("ServiceHealthPaths returned non-nil for an unknown template")
	}
}

func TestRegistry_Materialize(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	dest := t.TempDir()
	if err := r.Materialize("grafana", dest); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	// Template files land; the manifest does not.
	for _, want := range []string{
		"compose.yaml",
		filepath.Join("provisioning", "dashboards", "dashboards.yaml"),
		filepath.Join("provisioning", "dashboards", "vac-welcome.json"),
	} {
		if _, err := os.Stat(filepath.Join(dest, want)); err != nil {
			t.Errorf("expected %s materialized: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "manifest.json")); !os.IsNotExist(err) {
		t.Error("manifest.json should not be materialized")
	}
}

func TestRegistry_MaterializeUnknown(t *testing.T) {
	r, _ := NewRegistry()
	if err := r.Materialize("nope", t.TempDir()); err == nil {
		t.Error("expected error materializing an unknown template")
	}
}
