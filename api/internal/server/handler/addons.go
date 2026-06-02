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
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// AddonCatalog reads the embedded add-on catalog. *addon.Registry satisfies it.
type AddonCatalog interface {
	List() []addon.Template
	Get(id string) (addon.Template, bool)
}

// AddonInstaller installs a catalog template as an app. *addon.Installer
// satisfies it.
type AddonInstaller interface {
	Install(ctx context.Context, templateID, name, slug string) (addon.InstallResult, error)
}

// ListAddons returns the catalog.
func ListAddons(cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, cat.List())
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
		WriteJSON(w, http.StatusOK, t)
	}
}

type installAddonReq struct {
	Name string `json:"name"`
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

		res, err := installer.Install(r.Context(), id, name, slug)
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
