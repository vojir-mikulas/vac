package addon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

var (
	// ErrUnknownTemplate is returned for an install of a non-existent template id.
	ErrUnknownTemplate = errors.New("addon: unknown template")
	// ErrEncryptionDisabled is returned when a template needs env vars but
	// VAC_MASTER_KEY is unset (env values are sealed at rest).
	ErrEncryptionDisabled = errors.New("addon: encryption disabled (VAC_MASTER_KEY unset)")
)

// randomSentinel in a template's default_env value is replaced with a generated
// secret at install time (e.g. an admin password), returned once to the operator.
const randomSentinel = "@random"

// Enqueuer enqueues a deployment for the worker. *deploy.Worker satisfies it.
type Enqueuer interface {
	Enqueue(deploymentID string) error
}

// DBProvisioner provisions a managed database for a template that depends on one.
// *dbprovision.Provisioner satisfies it; nil disables DB-dependent templates.
type DBProvisioner interface {
	Add(ctx context.Context, app store.App, engine, envVarName string) (store.ManagedDatabase, error)
}

// Installer turns a catalog template into a running app: create a template app,
// inject default env (generating @random secrets), provision a managed DB if the
// template depends on one, and enqueue the first deploy. The deploy pipeline
// materializes the embedded files (the clone-step seam).
type Installer struct {
	store    *store.Store
	box      *crypto.Box
	registry *Registry
	worker   Enqueuer
	dbProv   DBProvisioner
	logger   *slog.Logger
}

// NewInstaller wires the installer. dbProv may be nil (DB-dependent templates
// then refuse).
func NewInstaller(s *store.Store, box *crypto.Box, registry *Registry, worker Enqueuer, dbProv DBProvisioner, logger *slog.Logger) *Installer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Installer{store: s, box: box, registry: registry, worker: worker, dbProv: dbProv, logger: logger}
}

// InstallResult carries the new app plus any secrets generated during install
// (shown to the operator once — they're sealed at rest and not re-derivable).
type InstallResult struct {
	App              store.App
	Deployment       store.Deployment
	GeneratedSecrets map[string]string
}

// Install provisions and enqueues a template install. slug must be a
// pre-validated, unique app slug (the handler derives it).
//
// envOverrides lets the operator supply their own values for the template's
// default env (e.g. an admin user/password) instead of accepting the defaults.
// Only keys the template declares are honored; blank overrides fall back to the
// default (and a blank override for a @random field still gets a generated
// secret). Operator-supplied values are NOT returned in GeneratedSecrets — only
// the ones VAC generated are surfaced once.
func (in *Installer) Install(ctx context.Context, templateID, name, slug string, envOverrides map[string]string) (InstallResult, error) {
	tmpl, ok := in.registry.Get(templateID)
	if !ok {
		return InstallResult{}, ErrUnknownTemplate
	}
	if len(tmpl.DefaultEnv) > 0 && in.box == nil {
		return InstallResult{}, ErrEncryptionDisabled
	}

	app, err := in.store.CreateTemplateApp(ctx, name, slug, templateID, tmpl.ComposeFile)
	if err != nil {
		return InstallResult{}, err
	}

	generated := map[string]string{}
	for k, v := range tmpl.DefaultEnv {
		if override, ok := envOverrides[k]; ok && strings.TrimSpace(override) != "" {
			// Operator chose their own value — use it verbatim, don't surface it
			// back (they already know it).
			v = override
		} else if v == randomSentinel {
			pw, gerr := randomPassword()
			if gerr != nil {
				return InstallResult{}, gerr
			}
			v = pw
			generated[k] = v
		}
		sealed, serr := in.box.Seal([]byte(v))
		if serr != nil {
			return InstallResult{}, fmt.Errorf("addon: seal env %s: %w", k, serr)
		}
		if err := in.store.UpsertEnvVar(ctx, app.ID, k, sealed, true); err != nil {
			return InstallResult{}, fmt.Errorf("addon: inject env %s: %w", k, err)
		}
	}

	// Provision a managed DB before first deploy if the template needs one — D2
	// injects DATABASE_URL asynchronously, picked up on the deploy that follows.
	if tmpl.DependsOnDB != "" {
		if in.dbProv == nil {
			in.logger.Warn("addon: template depends on a managed DB but provisioning is unavailable", "template", templateID)
		} else if _, derr := in.dbProv.Add(ctx, app, tmpl.DependsOnDB, ""); derr != nil {
			in.logger.Warn("addon: provision dependent DB", "template", templateID, "err", derr)
		}
	}

	d, err := in.store.CreateDeployment(ctx, app.ID, store.TriggeredManual, nil)
	if err != nil {
		return InstallResult{}, fmt.Errorf("addon: create deployment: %w", err)
	}
	if err := in.worker.Enqueue(d.ID); err != nil {
		return InstallResult{}, fmt.Errorf("addon: enqueue deploy: %w", err)
	}

	in.logger.Info("addon: installed", "template", templateID, "app", slug)
	return InstallResult{App: app, Deployment: d, GeneratedSecrets: generated}, nil
}

const pwAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomPassword() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("addon: rand: %w", err)
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = pwAlphabet[int(b)%len(pwAlphabet)]
	}
	return string(out), nil
}
