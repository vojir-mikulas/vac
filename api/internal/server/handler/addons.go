package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// AddonCatalog reads the embedded add-on catalog. *addon.Registry satisfies it.
type AddonCatalog interface {
	List() []addon.Template
	Get(id string) (addon.Template, bool)
}

// AddonEngineSource lists managed-DB engines to cross-list in the add-on
// catalog. *dbprovision.Provisioner satisfies it; nil omits database add-ons.
type AddonEngineSource interface {
	AvailableEngines() []dbprovision.EngineInfo
}

// AddonInstaller installs a catalog template as an app. *addon.Installer
// satisfies it.
type AddonInstaller interface {
	Install(ctx context.Context, templateID, name, slug string, envOverrides map[string]string) (addon.InstallResult, error)
}

// addonDTO is one catalog entry. kind="template" entries deploy as a normal app
// (compose + manifest); kind="database" entries cross-list a heavyweight
// managed-DB engine (e.g. MariaDB) so it's discoverable here — it's provisioned
// per app from an app's Database tab, not deployed as a standalone app.
type addonDTO struct {
	Kind        string            `json:"kind"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Icon        string            `json:"icon"`
	FootprintMB int               `json:"footprint_mb"`
	DependsOnDB string            `json:"depends_on_db"`
	ComposeFile string            `json:"compose_file,omitempty"`
	DefaultEnv  map[string]string `json:"default_env,omitempty"`
	// Shared marks a database add-on whose engine runs as one instance shared by
	// every app that provisions on it. Only meaningful for kind="database".
	Shared bool `json:"shared,omitempty"`
}

func templateAddon(t addon.Template) addonDTO {
	return addonDTO{
		Kind:        "template",
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Category:    t.Category,
		Icon:        t.Icon,
		FootprintMB: t.FootprintMB,
		DependsOnDB: t.DependsOnDB,
		ComposeFile: t.ComposeFile,
		DefaultEnv:  t.DefaultEnv,
	}
}

// dbEngineAddonMeta is presentation copy for managed-DB engines surfaced as
// add-ons. Footprint/shared come from the engine itself; this is just the label.
var dbEngineAddonMeta = map[string]struct{ Name, Description string }{
	"mariadb": {
		Name: "MariaDB",
		Description: "Managed MariaDB server. Add it to an app and VAC provisions a database, " +
			"injects the connection string as an env var, and schedules nightly backups. " +
			"One shared instance serves every app on this box.",
	},
	"redis": {
		Name: "Redis",
		Description: "Managed Redis cache. Add it to an app and VAC provisions an isolated keyspace — " +
			"your keys live under a private prefix (injected as REDIS_PREFIX, with the connection URL " +
			"as REDIS_URL) — served by one shared instance across every app on this box. It's treated " +
			"as a cache: persisted across restarts, but not covered by VAC's nightly database backups.",
	},
}

func dbEngineAddon(e dbprovision.EngineInfo) addonDTO {
	meta := dbEngineAddonMeta[e.Name]
	name := meta.Name
	if name == "" {
		name = strings.ToUpper(e.Name[:1]) + e.Name[1:]
	}
	return addonDTO{
		Kind:        "database",
		ID:          e.Name,
		Name:        name,
		Description: meta.Description,
		Category:    "Database",
		Icon:        e.Name, // brand-icon maps engine names ("mariadb", …)
		FootprintMB: e.FootprintMB,
		Shared:      e.Shared,
	}
}

// ListAddons returns the catalog: template add-ons plus any heavyweight
// managed-DB engines (cross-listed for discoverability). Free/built-in engines
// (Postgres, which lives in the shared control-plane vac-db) are not add-ons.
func ListAddons(cat AddonCatalog, engines AddonEngineSource) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list := cat.List()
		out := make([]addonDTO, 0, len(list)+2)
		for _, t := range list {
			out = append(out, templateAddon(t))
		}
		if engines != nil {
			for _, e := range engines.AvailableEngines() {
				if e.FootprintMB <= 0 {
					continue // free/built-in engines aren't add-ons
				}
				out = append(out, dbEngineAddon(e))
			}
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// GetAddon returns one template's detail (incl. footprint + DB dependency).
func GetAddon(cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := cat.Get(id)
		if !ok {
			WriteError(w, http.StatusNotFound, "add-on not found")
			return
		}
		WriteJSON(w, http.StatusOK, templateAddon(t))
	}
}

type installAddonReq struct {
	Name string `json:"name"`
	// Env lets the operator supply their own values for the template's default
	// env (e.g. an admin user/password). Unknown keys are ignored; blank values
	// fall back to the template default or a generated secret.
	Env map[string]string `json:"env,omitempty"`
}

type installResultDTO struct {
	AppID            string            `json:"app_id"`
	Slug             string            `json:"slug"`
	Name             string            `json:"name"`
	Status           string            `json:"status"`
	DeploymentID     string            `json:"deployment_id"`
	GeneratedSecrets map[string]string `json:"generated_secrets,omitempty"`
}

// InstallAddon installs a template as an app and enqueues its first deploy.
// Returns 202 with the new app; the UI then watches the deploy stream like any
// app. Generated secrets (e.g. an admin password) are returned once.
func InstallAddon(cat AddonCatalog, installer AddonInstaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := cat.Get(id)
		if !ok {
			WriteError(w, http.StatusNotFound, "add-on not found")
			return
		}
		var req installAddonReq
		// Body is optional — default the app name to the template name.
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = t.Name
		}
		slug := deriveSlug(name)
		if slug == "" {
			slug = id
		}

		res, err := installer.Install(r.Context(), id, name, slug, req.Env)
		if err != nil {
			switch {
			case errors.Is(err, addon.ErrUnknownTemplate):
				WriteError(w, http.StatusNotFound, "add-on not found")
			case errors.Is(err, addon.ErrEncryptionDisabled):
				WriteError(w, http.StatusUnprocessableEntity, "encryption is disabled (VAC_MASTER_KEY unset); add-ons need it")
			case errors.Is(err, store.ErrConflict):
				WriteError(w, http.StatusConflict, "an app with that name already exists — pick another name")
			default:
				WriteError(w, http.StatusInternalServerError, "could not install add-on")
			}
			return
		}
		audit.SetTarget(r.Context(), "app", res.App.ID)
		audit.Describe(r.Context(), "installed add-on "+id)
		WriteJSON(w, http.StatusAccepted, installResultDTO{
			AppID:            res.App.ID,
			Slug:             res.App.Slug,
			Name:             res.App.Name,
			Status:           res.App.Status,
			DeploymentID:     res.Deployment.ID,
			GeneratedSecrets: res.GeneratedSecrets,
		})
	}
}
