// Package addon is the add-on catalog (Track D / D3): a set of embedded
// templates deployed as normal user apps through the existing pipeline. A
// template is data — compose.yaml + manifest + a provisioning bundle — never
// per-add-on Go code (decision #7). Grafana is the flagship.
package addon

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed templates
var templatesFS embed.FS

const manifestName = "manifest.json"

// Template is a catalog entry parsed from a manifest.json.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	// Icon is a brand-icon key the UI maps to a glyph (e.g. "grafana"); empty
	// falls back to a generic add-on icon.
	Icon        string `json:"icon"`
	FootprintMB int    `json:"footprint_mb"`
	// DependsOnDB names a managed-DB engine to provision before first deploy
	// (e.g. "postgres"), or "" for none.
	DependsOnDB string            `json:"depends_on_db"`
	ComposeFile string            `json:"compose_file"`
	DefaultEnv  map[string]string `json:"default_env"`
	// HealthPaths declares the Caddy active-health-check path per service
	// (service name → path) for templates whose root "/" isn't a 2xx. Grafana,
	// for instance, 302-redirects "/" → "/login", which fails Caddy's 2xx-only
	// active check and leaves the upstream down (503); "/api/health" is its
	// unauthenticated 200 endpoint. The deploy pipeline applies these after the
	// first deploy creates the service rows.
	HealthPaths map[string]string `json:"health_paths"`
	// RequiresCPUBaseline names an x86-64 microarchitecture level the add-on's
	// image needs (currently "x86-64-v2"), or "" for none. MinIO's image is built
	// against a glibc that aborts on a CPU without x86-64-v2 (SSE4.2/POPCNT) — a
	// common case on budget VPS that expose a generic virtual CPU. The catalog
	// surfaces this as a requirement and, when the host can't meet it, an
	// up-front incompatibility flag instead of a post-install crash loop.
	RequiresCPUBaseline string `json:"requires_cpu_baseline"`
}

// Registry holds the parsed catalog. It also implements
// deploy.TemplateMaterializer.
type Registry struct {
	templates map[string]Template
	order     []string
}

// NewRegistry parses every embedded template manifest. It fails fast if a
// manifest is malformed — a broken template should surface at boot, not install.
func NewRegistry() (*Registry, error) {
	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		return nil, fmt.Errorf("addon: read templates: %w", err)
	}
	r := &Registry{templates: map[string]Template{}}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := templatesFS.ReadFile(path("templates", e.Name(), manifestName))
		if err != nil {
			return nil, fmt.Errorf("addon: read %s manifest: %w", e.Name(), err)
		}
		var t Template
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("addon: parse %s manifest: %w", e.Name(), err)
		}
		if t.ID == "" || t.ID != e.Name() {
			return nil, fmt.Errorf("addon: %s manifest id %q must match its directory", e.Name(), t.ID)
		}
		if t.ComposeFile == "" {
			t.ComposeFile = "compose.yaml"
		}
		r.templates[t.ID] = t
		r.order = append(r.order, t.ID)
	}
	sort.Strings(r.order)
	return r, nil
}

// List returns the catalog in a stable order.
func (r *Registry) List() []Template {
	out := make([]Template, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.templates[id])
	}
	return out
}

// Get returns one template by id.
func (r *Registry) Get(id string) (Template, bool) {
	t, ok := r.templates[id]
	return t, ok
}

// ServiceHealthPaths returns the template's declared per-service Caddy
// health-check paths (service name → path), or nil if it declares none. The
// deploy pipeline applies these after the first deploy so add-ons whose root
// path isn't a 2xx (e.g. Grafana → 302) still pass Caddy's active health check.
func (r *Registry) ServiceHealthPaths(templateID string) map[string]string {
	t, ok := r.templates[templateID]
	if !ok {
		return nil
	}
	return t.HealthPaths
}

// Materialize copies a template's files (everything but the manifest) into
// destDir, preserving structure — the deploy clone-step replacement for a git
// clone. Overwrites existing files so a redeploy refreshes the template.
func (r *Registry) Materialize(templateID, destDir string) error {
	if _, ok := r.templates[templateID]; !ok {
		return fmt.Errorf("addon: unknown template %q", templateID)
	}
	root := path("templates", templateID)
	return fs.WalkDir(templatesFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		if rel == "" || rel == manifestName {
			return nil
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		data, err := templatesFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o640)
	})
}

// path joins embed paths with forward slashes (embed.FS always uses "/").
func path(parts ...string) string { return strings.Join(parts, "/") }
