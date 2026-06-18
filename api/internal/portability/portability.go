// Package portability is the application service behind VAC's import on-ramp and
// export exit-ramp (plan 18, Phase 2). It orchestrates the store + crypto work
// that surrounds the pure translation in package appspec: gathering an app's rows
// and decrypting non-sensitive env for export, and creating/updating the app +
// its services, domains, triggers, and (re-sealed) env for import.
//
// It is shared by the HTTP handlers and the `vac-api` CLI so both directions
// behave identically. It deliberately does NOT depend on the proxy/Caddy stack:
// import only writes the DB (the next deploy reconciles routing), which also keeps
// the CLI binary lean. Route syncing, audit, and HTTP/CLI concerns live in the
// callers.
package portability

import (
	"context"
	"errors"
	"fmt"

	"github.com/vojir-mikulas/vac/api/internal/appspec"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// serviceStatusCreated is the lifecycle status a pre-created (not-yet-deployed)
// service row carries — the same default the schema and apps row use. The deploy
// pipeline reconciles it to a real status on first up.
const serviceStatusCreated = "created"

// ErrMasterKeyRequired is returned when an operation needs VAC_MASTER_KEY but the
// crypto box is unavailable: exporting non-sensitive env values (which are sealed
// at rest) or importing any env at all (values must be sealed before storage).
var ErrMasterKeyRequired = errors.New("portability: VAC_MASTER_KEY is required to seal/unseal env values")

// Export gathers an app's portable configuration into a Spec. Sensitive env
// values are omitted (keys + sensitivity only); non-sensitive values are
// decrypted and included, which requires the master key. Runtime state is dropped
// by appspec.FromApp. Returns store.ErrNotFound when the app doesn't exist.
func Export(ctx context.Context, st *store.Store, box *crypto.Box, appID string) (appspec.Spec, error) {
	app, err := st.GetApp(ctx, appID)
	if err != nil {
		return appspec.Spec{}, err
	}
	services, err := st.ListServicesForApp(ctx, appID)
	if err != nil {
		return appspec.Spec{}, err
	}
	domains, err := st.ListDomainsByApp(ctx, appID)
	if err != nil {
		return appspec.Spec{}, err
	}
	triggers, err := st.ListDeployTriggers(ctx, appID)
	if err != nil {
		return appspec.Spec{}, err
	}
	rows, err := st.ListEnvVarsForApp(ctx, appID)
	if err != nil {
		return appspec.Spec{}, err
	}

	env := make([]appspec.EnvVar, 0, len(rows))
	for _, v := range rows {
		e := appspec.EnvVar{Key: v.Key, Sensitive: v.Sensitive}
		if !v.Sensitive {
			if box == nil {
				return appspec.Spec{}, ErrMasterKeyRequired
			}
			plain, err := box.Open(v.Value)
			if err != nil {
				return appspec.Spec{}, fmt.Errorf("portability: decrypt env %q: %w", v.Key, err)
			}
			e.Value = string(plain)
		}
		env = append(env, e)
	}

	// The deploy key's public half is informational (the destination regenerates
	// its own); fetch best-effort. Its absence is not an error.
	var sshPub string
	if k, err := st.GetSSHKeyForApp(ctx, appID); err == nil {
		sshPub = k.PublicKey
	}

	return appspec.FromApp(appspec.FromAppInput{
		App:          app,
		Services:     services,
		Domains:      domains,
		Triggers:     triggers,
		Env:          env,
		SSHPublicKey: sshPub,
	}), nil
}

// ImportResult summarizes what an import created or updated. SecretsNeeded lists
// the sensitive env keys that were imported without a value (the operator must
// re-paste them before those settings take effect).
type ImportResult struct {
	AppID         string   `json:"app_id"`
	Slug          string   `json:"slug"`
	Created       bool     `json:"created"`
	Services      int      `json:"services"`
	Domains       int      `json:"domains"`
	Triggers      int      `json:"triggers"`
	EnvVars       int      `json:"env_vars"`
	SecretsNeeded []string `json:"secrets_needed,omitempty"`
}

// InvalidSpecError wraps a spec validation failure so callers can map it to a 400
// (vs. a 500 for a genuine store error).
type InvalidSpecError struct{ Err error }

func (e InvalidSpecError) Error() string { return e.Err.Error() }
func (e InvalidSpecError) Unwrap() error { return e.Err }

// Import applies a Spec to the store, idempotent on slug: it creates a new app or
// updates the existing one in place (the instance-migration path). Services are
// pre-created so domains can bind to them and operator-set config (internal port,
// health path) survives; domains, triggers, and env are replaced to match the
// spec exactly (the spec is the desired state). Env values are re-sealed under the
// destination key. The whole sequence is best-effort sequential, not a single
// transaction — a mid-way failure leaves a partial app that a re-import (same
// slug → update) repairs.
func Import(ctx context.Context, st *store.Store, box *crypto.Box, spec appspec.Spec) (ImportResult, error) {
	in, err := appspec.ToApp(spec)
	if err != nil {
		return ImportResult{}, InvalidSpecError{Err: err}
	}
	if len(in.Env) > 0 && box == nil {
		return ImportResult{}, ErrMasterKeyRequired
	}

	existing, err := st.GetAppBySlug(ctx, in.Slug)
	var appID string
	var created bool
	switch {
	case err == nil:
		appID = existing.ID
		if err := updateApp(ctx, st, existing, in); err != nil {
			return ImportResult{}, fmt.Errorf("portability: update app: %w", err)
		}
	case errors.Is(err, store.ErrNotFound):
		a, err := createApp(ctx, st, in)
		if err != nil {
			return ImportResult{}, fmt.Errorf("portability: create app: %w", err)
		}
		appID, created = a.ID, true
	default:
		return ImportResult{}, err
	}

	if err := applyServices(ctx, st, appID, in.Services); err != nil {
		return ImportResult{}, err
	}
	if err := replaceDomains(ctx, st, appID, in.Domains); err != nil {
		return ImportResult{}, err
	}
	if err := replaceTriggers(ctx, st, appID, in.Triggers); err != nil {
		return ImportResult{}, err
	}
	needed, err := replaceEnv(ctx, st, box, appID, in.Env)
	if err != nil {
		return ImportResult{}, err
	}

	return ImportResult{
		AppID:         appID,
		Slug:          in.Slug,
		Created:       created,
		Services:      len(in.Services),
		Domains:       len(in.Domains),
		Triggers:      len(in.Triggers),
		EnvVars:       len(in.Env),
		SecretsNeeded: needed,
	}, nil
}

func createApp(ctx context.Context, st *store.Store, in appspec.AppInputs) (store.App, error) {
	var (
		a   store.App
		err error
	)
	if in.Source == appspec.SourceTemplate {
		a, err = st.CreateTemplateApp(ctx, in.Name, in.Slug, in.TemplateID, in.ComposeFile)
	} else {
		a, err = st.CreateApp(ctx, in.Name, in.Slug, in.GitURL, in.GitBranch, in.ComposeFile, in.BuildKind, in.BuildConfig)
	}
	if err != nil {
		return store.App{}, err
	}
	// CreateApp leaves mem_limit_mb NULL (unlimited); set it only when the spec
	// asks for a ceiling.
	if in.MemLimitMB != nil {
		if _, err := st.UpdateApp(ctx, a.ID, nil, nil, nil, nil, nil, nil, in.MemLimitMB, nil); err != nil {
			return store.App{}, err
		}
	}
	return a, nil
}

func updateApp(ctx context.Context, st *store.Store, existing store.App, in appspec.AppInputs) error {
	// Spec is the desired state: an omitted mem limit means "unlimited", so on
	// update we explicitly clear it (0 → NULL) rather than leaving the prior value.
	mem := desiredMemLimit(in.MemLimitMB)
	if in.Source == appspec.SourceTemplate {
		// Template apps keep their git-less/compose wiring; only the operator-facing
		// fields are patched.
		_, err := st.UpdateApp(ctx, existing.ID, &in.Name, nil, nil, &in.ComposeFile, nil, nil, mem, nil)
		return err
	}
	_, err := st.UpdateApp(ctx, existing.ID, &in.Name, &in.GitURL, &in.GitBranch, &in.ComposeFile, &in.BuildKind, in.BuildConfig, mem, nil)
	return err
}

// desiredMemLimit maps a spec mem limit onto UpdateApp's pointer semantics: a set
// value is applied; an omitted limit becomes an explicit 0, which UpdateApp maps
// to SQL NULL (clear → unlimited).
func desiredMemLimit(p *int) *int {
	if p == nil {
		zero := 0
		return &zero
	}
	return p
}

// applyServices pre-creates each declared service (so domains can bind and
// operator config survives) without clobbering a running service's status: a new
// service is inserted with the default status, an existing one has only its
// config columns updated.
func applyServices(ctx context.Context, st *store.Store, appID string, services []appspec.ServiceInput) error {
	for _, svc := range services {
		_, err := st.GetService(ctx, appID, svc.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// has_volumes false here; recomputed from compose on the next deploy.
			if _, err := st.UpsertService(ctx, appID, svc.Name, nil, nil, svc.InternalPort, serviceStatusCreated, false); err != nil {
				return fmt.Errorf("portability: create service %q: %w", svc.Name, err)
			}
			if svc.HealthPath != nil {
				if _, err := st.SetServiceConfig(ctx, appID, svc.Name, nil, nil, svc.HealthPath); err != nil {
					return fmt.Errorf("portability: service %q health path: %w", svc.Name, err)
				}
			}
		case err == nil:
			if _, err := st.SetServiceConfig(ctx, appID, svc.Name, nil, svc.InternalPort, svc.HealthPath); err != nil {
				return fmt.Errorf("portability: update service %q: %w", svc.Name, err)
			}
		default:
			return fmt.Errorf("portability: load service %q: %w", svc.Name, err)
		}
	}
	return nil
}

// replaceDomains makes the app's domain set match the spec: drop the app-owned
// rows, then recreate from the spec. A redirect domain is created then patched
// with its target (CreateDomain doesn't carry redirect_to). Unassigned domains
// (no service) are stored with a NULL app/service to satisfy the both-or-neither
// constraint.
func replaceDomains(ctx context.Context, st *store.Store, appID string, domains []appspec.DomainInput) error {
	existing, err := st.ListDomainsByApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("portability: list domains: %w", err)
	}
	for _, d := range existing {
		if err := st.DeleteDomain(ctx, appID, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("portability: prune domain %q: %w", d.Hostname, err)
		}
	}
	for _, d := range domains {
		scopeApp, scopeSvc := appID, d.ServiceName
		if scopeSvc == "" {
			scopeApp = "" // unassigned: both app and service must be NULL
		}
		created, err := st.CreateDomain(ctx, scopeApp, scopeSvc, d.Hostname, d.Type)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				return InvalidSpecError{Err: fmt.Errorf("hostname %q is already in use", d.Hostname)}
			}
			return fmt.Errorf("portability: create domain %q: %w", d.Hostname, err)
		}
		if d.RedirectTo != "" {
			if _, err := st.UpdateDomain(ctx, created.ID, scopeApp, scopeSvc, d.Hostname, d.RedirectTo); err != nil {
				return fmt.Errorf("portability: set redirect on %q: %w", d.Hostname, err)
			}
		}
	}
	return nil
}

func replaceTriggers(ctx context.Context, st *store.Store, appID string, triggers []appspec.TriggerInput) error {
	existing, err := st.ListDeployTriggers(ctx, appID)
	if err != nil {
		return fmt.Errorf("portability: list triggers: %w", err)
	}
	for _, t := range existing {
		if err := st.DeleteDeployTrigger(ctx, appID, t.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("portability: prune trigger: %w", err)
		}
	}
	for _, t := range triggers {
		// require_approval is an operational gate, not part of the portable app
		// spec — imported triggers default to no approval requirement.
		if _, err := st.CreateDeployTrigger(ctx, appID, t.Event, t.Filter, false); err != nil {
			return fmt.Errorf("portability: create trigger %q: %w", t.Event, err)
		}
	}
	return nil
}

// replaceEnv re-seals and replaces the app's env set. A sensitive entry imported
// without a value is sealed as empty (a placeholder the operator overwrites) and
// reported in the returned list. Callers guarantee box != nil when env is present.
func replaceEnv(ctx context.Context, st *store.Store, box *crypto.Box, appID string, env []appspec.EnvVar) ([]string, error) {
	inputs := make([]store.EnvVarInput, 0, len(env))
	var needed []string
	for _, e := range env {
		sealed, err := box.Seal([]byte(e.Value))
		if err != nil {
			return nil, fmt.Errorf("portability: seal env %q: %w", e.Key, err)
		}
		inputs = append(inputs, store.EnvVarInput{Key: e.Key, Value: sealed, Sensitive: e.Sensitive})
		if e.Sensitive && e.Value == "" {
			needed = append(needed, e.Key)
		}
	}
	if err := st.ReplaceEnvVars(ctx, appID, inputs); err != nil {
		return nil, fmt.Errorf("portability: save env: %w", err)
	}
	return needed, nil
}
