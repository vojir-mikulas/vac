package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeEngines is an AddonEngineSource for cross-listing managed-DB engines.
type fakeEngines struct{ engines []dbprovision.EngineInfo }

func (f fakeEngines) AvailableEngines() []dbprovision.EngineInfo { return f.engines }

type fakeCatalog struct {
	templates map[string]addon.Template
}

func (f *fakeCatalog) List() []addon.Template {
	out := make([]addon.Template, 0, len(f.templates))
	for _, t := range f.templates {
		out = append(out, t)
	}
	return out
}

func (f *fakeCatalog) Get(id string) (addon.Template, bool) {
	t, ok := f.templates[id]
	return t, ok
}

type fakeInstaller struct {
	gotTemplate, gotName, gotSlug string
	gotEnv                        map[string]string
	result                        addon.InstallResult
}

func (f *fakeInstaller) Install(_ context.Context, templateID, name, slug string, envOverrides map[string]string) (addon.InstallResult, error) {
	f.gotTemplate, f.gotName, f.gotSlug, f.gotEnv = templateID, name, slug, envOverrides
	return f.result, nil
}

func grafanaCatalog() *fakeCatalog {
	return &fakeCatalog{templates: map[string]addon.Template{
		"grafana": {ID: "grafana", Name: "Grafana", FootprintMB: 150, ComposeFile: "compose.yaml"},
	}}
}

func TestListAddons(t *testing.T) {
	rr := httptest.NewRecorder()
	ListAddons(grafanaCatalog(), nil)(rr, httptest.NewRequest(http.MethodGet, "/api/addons", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got []addonDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "grafana" || got[0].Kind != "template" {
		t.Errorf("catalog = %+v", got)
	}
}

// Heavyweight engines (MariaDB) are cross-listed as database add-ons; free
// engines (Postgres, footprint 0) are not.
func TestListAddons_CrossListsHeavyweightEngines(t *testing.T) {
	engines := fakeEngines{engines: []dbprovision.EngineInfo{
		{Name: "postgres", FootprintMB: 0, Shared: false},
		{Name: "mariadb", FootprintMB: 150, Shared: true},
	}}
	rr := httptest.NewRecorder()
	ListAddons(grafanaCatalog(), engines)(rr, httptest.NewRequest(http.MethodGet, "/api/addons", nil))

	var got []addonDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var db *addonDTO
	for i := range got {
		if got[i].Kind == "database" {
			if db != nil {
				t.Fatalf("more than one database add-on: %+v", got)
			}
			db = &got[i]
		}
	}
	if db == nil {
		t.Fatalf("expected a database add-on, got %+v", got)
	}
	if db.ID != "mariadb" || db.Name != "MariaDB" || db.FootprintMB != 150 || !db.Shared {
		t.Errorf("database add-on = %+v", db)
	}
}

func TestGetAddon_NotFound(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/addons/{id}", GetAddon(grafanaCatalog()))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/addons/unknown", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestInstallAddon_Success(t *testing.T) {
	inst := &fakeInstaller{result: addon.InstallResult{
		App:              store.App{ID: "app1", Slug: "grafana", Name: "Grafana", Status: "building"},
		Deployment:       store.Deployment{ID: "dep1"},
		GeneratedSecrets: map[string]string{"GF_ADMIN_PASSWORD": "s3cret"},
	}}
	r := chi.NewRouter()
	r.Post("/addons/{id}/install", InstallAddon(grafanaCatalog(), inst))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/addons/grafana/install", strings.NewReader(`{"name":"My Grafana"}`))
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	if inst.gotTemplate != "grafana" || inst.gotName != "My Grafana" || inst.gotSlug != "my-grafana" {
		t.Errorf("installer got template=%q name=%q slug=%q", inst.gotTemplate, inst.gotName, inst.gotSlug)
	}
	var body installResultDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.DeploymentID != "dep1" || body.GeneratedSecrets["GF_ADMIN_PASSWORD"] != "s3cret" {
		t.Errorf("install result = %+v", body)
	}
}

func TestInstallAddon_UnknownTemplate(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/addons/{id}/install", InstallAddon(grafanaCatalog(), &fakeInstaller{}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/addons/unknown/install", strings.NewReader(`{}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
