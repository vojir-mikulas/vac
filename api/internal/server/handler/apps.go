package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const (
	defaultBranch      = "main"
	defaultComposeFile = "compose.yaml"
	maxAppNameLen      = 100
	maxSlugLen         = 63
	// minMemLimitMB is the smallest per-app RAM limit we accept — below this a
	// container can't realistically start, so a smaller value is a typo.
	minMemLimitMB = 6
)

// gitURLRe matches SSH (git@host:path), ssh:// or http(s):// repository URLs.
// It is intentionally permissive on the path — we only reject obviously
// malformed inputs here; clone-time failure is the real validator.
var gitURLRe = regexp.MustCompile(`^(?:https?://\S+|git@[^\s:]+:\S+|ssh://git@\S+/\S+)$`)

// slugRe is the on-the-wire format we accept and store. Lowercase alnum
// segments separated by hyphens. Same shape as Kubernetes / Docker names.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// gitRefRe is the subset of git ref names we accept. Conservative on purpose:
// git's own ref-name grammar allows characters that have ambiguous shell /
// option-flag interpretations (e.g. leading `-`). Reject anything outside
// `[A-Za-z0-9._/-]` and any ref that starts with `-` (would be parsed as a
// flag by git).
var gitRefRe = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]*$`)

type createAppRequest struct {
	Name        string          `json:"name"                    validate:"required,min=1,max=100"`
	Slug        string          `json:"slug,omitempty"          validate:"omitempty,max=63"`
	GitURL      string          `json:"git_url"                 validate:"required,min=1,max=500"`
	GitBranch   string          `json:"git_branch,omitempty"    validate:"omitempty,max=200"`
	ComposeFile string          `json:"compose_file,omitempty"  validate:"omitempty,max=200"`
	BuildKind   string          `json:"build_kind,omitempty"    validate:"omitempty,max=32"`
	BuildConfig json.RawMessage `json:"build_config,omitempty"`
}

type updateAppRequest struct {
	Name        *string         `json:"name,omitempty"`
	GitURL      *string         `json:"git_url,omitempty"`
	GitBranch   *string         `json:"git_branch,omitempty"`
	ComposeFile *string         `json:"compose_file,omitempty"`
	BuildKind   *string         `json:"build_kind,omitempty"`
	BuildConfig json.RawMessage `json:"build_config,omitempty"`
	// MemLimitMB: nil leaves the limit unchanged; 0 clears it (unlimited);
	// a positive value sets the per-app RAM ceiling in MiB (plan 06).
	MemLimitMB *int `json:"mem_limit_mb,omitempty"`
}

type appDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	GitURL      string          `json:"git_url"`
	GitBranch   string          `json:"git_branch"`
	ComposeFile string          `json:"compose_file"`
	BuildKind   string          `json:"build_kind"`
	BuildConfig json.RawMessage `json:"build_config"`
	Status      string          `json:"status"`
	MemLimitMB  *int            `json:"mem_limit_mb"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// Source is "git" or "template"; TemplateID/Name/Icon are populated for
	// add-on (template-sourced) apps so the UI can render them distinctly from
	// git apps (Stage 0 shared seam — see docs/plans/triage/00-parallel-tracks.md).
	Source       string  `json:"source"`
	TemplateID   *string `json:"template_id"`
	TemplateName *string `json:"template_name,omitempty"`
	TemplateIcon *string `json:"template_icon,omitempty"`
}

// toAppDTO maps a store row to the wire DTO. cat (nil-able) resolves an add-on
// template's display name + icon for template-sourced apps; git apps ignore it.
func toAppDTO(a store.App, cat AddonCatalog) appDTO {
	bc := a.BuildConfig
	if len(bc) == 0 {
		bc = json.RawMessage("{}")
	}
	d := appDTO{
		ID:          a.ID,
		Name:        a.Name,
		Slug:        a.Slug,
		GitURL:      a.GitURL,
		GitBranch:   a.GitBranch,
		ComposeFile: a.ComposeFile,
		BuildKind:   a.BuildKind,
		BuildConfig: bc,
		Status:      a.Status,
		MemLimitMB:  a.MemLimitMB,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
		Source:      a.Source,
		TemplateID:  a.TemplateID,
	}
	if a.Source == store.AppSourceTemplate && a.TemplateID != nil && cat != nil {
		if t, ok := cat.Get(*a.TemplateID); ok {
			name := t.Name
			d.TemplateName = &name
			if t.Icon != "" {
				icon := t.Icon
				d.TemplateIcon = &icon
			}
		}
	}
	return d
}

// validBuildKinds is the set accepted on the wire.
var validBuildKinds = map[string]bool{
	adapter.KindAuto:       true,
	adapter.KindCompose:    true,
	adapter.KindDockerfile: true,
	adapter.KindFramework:  true,
	adapter.KindStatic:     true,
}

// normalizeBuildConfig validates a (kind, raw build_config) pair and returns
// the canonical JSON to persist (unknown fields dropped). A blank raw → "{}".
func normalizeBuildConfig(kind string, raw json.RawMessage) (json.RawMessage, string, bool) {
	cfg, err := adapter.ParseConfig(raw)
	if err != nil {
		return nil, "invalid build_config json", false
	}
	if err := adapter.Validate(kind, cfg); err != nil {
		return nil, err.Error(), false
	}
	canonical, err := json.Marshal(cfg)
	if err != nil {
		return nil, "invalid build_config", false
	}
	return canonical, "", true
}

// CreateApp persists a new app record. Slug is derived from Name when not
// provided; a collision returns 409.
func CreateApp(s *store.Store, cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAppRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		// Trim before validation so a whitespace-only name is treated as empty.
		req.Name = strings.TrimSpace(req.Name)
		req.GitURL = strings.TrimSpace(req.GitURL)
		req.Slug = strings.TrimSpace(req.Slug)
		req.GitBranch = strings.TrimSpace(req.GitBranch)
		req.ComposeFile = strings.TrimSpace(req.ComposeFile)
		if msg, ok := validateStruct(req); !ok {
			WriteError(w, http.StatusBadRequest, msg)
			return
		}
		if !gitURLRe.MatchString(req.GitURL) {
			WriteError(w, http.StatusBadRequest, "git_url must be an https:// or git@ SSH URL")
			return
		}

		slug := req.Slug
		if slug == "" {
			slug = deriveSlug(req.Name)
		}
		if slug == "" || !slugRe.MatchString(slug) || len(slug) > maxSlugLen {
			WriteError(w, http.StatusBadRequest, "slug must be lowercase alphanumeric segments separated by '-'")
			return
		}

		branch := req.GitBranch
		if branch == "" {
			branch = defaultBranch
		}
		if !gitRefRe.MatchString(branch) {
			WriteError(w, http.StatusBadRequest, "git_branch must match "+gitRefRe.String())
			return
		}
		composeFile := req.ComposeFile
		if composeFile == "" {
			composeFile = defaultComposeFile
		}

		buildKind := strings.TrimSpace(req.BuildKind)
		if buildKind == "" {
			buildKind = adapter.KindAuto
		}
		if !validBuildKinds[buildKind] {
			WriteError(w, http.StatusBadRequest, "build_kind must be one of auto, compose, dockerfile, framework, static")
			return
		}
		buildConfig, msg, ok := normalizeBuildConfig(buildKind, req.BuildConfig)
		if !ok {
			WriteError(w, http.StatusBadRequest, msg)
			return
		}

		a, err := s.CreateApp(r.Context(), req.Name, slug, req.GitURL, branch, composeFile, buildKind, buildConfig)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "slug already in use")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create app")
			return
		}
		WriteJSON(w, http.StatusCreated, toAppDTO(a, cat))
	}
}

func ListApps(s *store.Store, cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListApps(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list apps")
			return
		}
		out := make([]appDTO, 0, len(rows))
		for _, a := range rows {
			out = append(out, toAppDTO(a, cat))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

func GetApp(s *store.Store, cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		a, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		WriteJSON(w, http.StatusOK, toAppDTO(a, cat))
	}
}

// UpdateApp applies a partial JSON patch. Slug is read-only here — once
// chosen, the slug is the app's stable handle.
func UpdateApp(s *store.Store, cat AddonCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req updateAppRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Name != nil {
			trimmed := strings.TrimSpace(*req.Name)
			if trimmed == "" || len(trimmed) > maxAppNameLen {
				WriteError(w, http.StatusBadRequest, "name must be 1..100 chars")
				return
			}
			req.Name = &trimmed
		}
		if req.GitURL != nil {
			trimmed := strings.TrimSpace(*req.GitURL)
			if !gitURLRe.MatchString(trimmed) {
				WriteError(w, http.StatusBadRequest, "git_url must be an https:// or git@ SSH URL")
				return
			}
			req.GitURL = &trimmed
		}
		if req.GitBranch != nil {
			trimmed := strings.TrimSpace(*req.GitBranch)
			if trimmed == "" {
				WriteError(w, http.StatusBadRequest, "git_branch cannot be empty")
				return
			}
			if !gitRefRe.MatchString(trimmed) {
				WriteError(w, http.StatusBadRequest, "git_branch must match "+gitRefRe.String())
				return
			}
			req.GitBranch = &trimmed
		}
		if req.ComposeFile != nil {
			trimmed := strings.TrimSpace(*req.ComposeFile)
			if trimmed == "" {
				WriteError(w, http.StatusBadRequest, "compose_file cannot be empty")
				return
			}
			req.ComposeFile = &trimmed
		}
		if req.BuildKind != nil {
			trimmed := strings.TrimSpace(*req.BuildKind)
			if !validBuildKinds[trimmed] {
				WriteError(w, http.StatusBadRequest, "build_kind must be one of auto, compose, dockerfile, framework, static")
				return
			}
			req.BuildKind = &trimmed
		}
		// Validate build_config against the kind being set (or, when the kind is
		// unchanged in this request, structurally). Persist the canonical form.
		var buildConfig json.RawMessage
		if req.BuildConfig != nil {
			kindForValidation := adapter.KindAuto
			if req.BuildKind != nil {
				kindForValidation = *req.BuildKind
			}
			canonical, msg, ok := normalizeBuildConfig(kindForValidation, req.BuildConfig)
			if !ok {
				WriteError(w, http.StatusBadRequest, msg)
				return
			}
			buildConfig = canonical
		}

		// RAM limit: 0 clears it; a positive value must be a sane floor so a
		// typo can't pin an app to a few MiB and wedge it in a restart loop.
		if req.MemLimitMB != nil && *req.MemLimitMB != 0 && *req.MemLimitMB < minMemLimitMB {
			WriteError(w, http.StatusBadRequest, "mem_limit_mb must be 0 (unlimited) or at least "+strconv.Itoa(minMemLimitMB))
			return
		}

		// Curated-revert snapshot: capture the full prior config so this patch can
		// be undone. Best-effort — a read failure must not block the update.
		if prior, err := s.GetApp(r.Context(), id); err == nil {
			audit.SetTarget(r.Context(), "app", id)
			audit.Describe(r.Context(), "updated configuration for "+prior.Slug)
			audit.Snapshot(r.Context(), map[string]any{"app": appConfigSnapshot(prior)})
		}

		a, err := s.UpdateApp(r.Context(), id, req.Name, req.GitURL, req.GitBranch, req.ComposeFile, req.BuildKind, buildConfig, req.MemLimitMB)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update app")
			return
		}
		WriteJSON(w, http.StatusOK, toAppDTO(a, cat))
	}
}

// appConfigSnapshot is the before-state stored for a revertable app-config
// update. Every field is the prior value (pointers so the reverter feeds them
// straight back into UpdateApp's partial-patch shape). Mirrors revert.AppSnapshot.
func appConfigSnapshot(a store.App) map[string]any {
	bc := a.BuildConfig
	if len(bc) == 0 {
		bc = json.RawMessage("{}")
	}
	return map[string]any{
		"name":         a.Name,
		"git_url":      a.GitURL,
		"git_branch":   a.GitBranch,
		"compose_file": a.ComposeFile,
		"build_kind":   a.BuildKind,
		"build_config": bc,
		"mem_limit_mb": a.MemLimitMB,
	}
}

// AppDBDeprovisioner drops the engine-side objects of an app's managed databases
// before the app row (and its cascade) is deleted. *dbprovision.Provisioner
// satisfies it. May be nil when managed services are off.
type AppDBDeprovisioner interface {
	DeprovisionApp(ctx context.Context, appID string)
}

// DeployInterrupter cancels an app's in-flight deployment when the app is
// deleted. Without it the worker keeps running the pipeline against torn-down
// infra (the build/up/health-gate never re-checks that the app still exists)
// until the per-deploy timeout fires ~30 min later — and with the default
// concurrency=1 that one wedged slot blocks every queued deploy. *deploy.Worker
// satisfies it; nil (tests / no deploy surface) disables the interrupt.
type DeployInterrupter interface {
	Cancel(deploymentID string) bool
	NotifyChanged()
}

func DeleteApp(s *store.Store, pm ProxyManager, dbDeprov AppDBDeprovisioner, ctrl AppStackController, worker DeployInterrupter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		// Interrupt any in-flight deploy first — its deploy context dies, so the
		// worker frees its pool slot promptly instead of grinding the pipeline
		// against the infra we're about to tear down. Capture the ids before the
		// cascade below removes the deployment rows. Best-effort: a lookup miss
		// must not block the delete.
		if worker != nil {
			if dids, derr := s.ActiveDeploymentIDsForApp(r.Context(), id); derr != nil {
				slog.Warn("delete: could not list active deployments", "app", app.ID, "err", derr)
			} else {
				for _, did := range dids {
					worker.Cancel(did)
				}
			}
		}
		// Stop and remove the app's containers + named volumes. Deleting an app is
		// permanent, so its data goes with it (e.g. an add-on's data volume).
		// Best-effort: a stuck stack must not block the delete.
		if ctrl != nil {
			if err := ctrl.Down(r.Context(), "vac-"+app.Slug, true); err != nil {
				slog.Warn("delete: could not down stack", "app", app.ID, "err", err)
			}
		}
		// Drop managed-database engine-side objects (DBs/roles inside the shared
		// instances) before the cascade removes the rows that point at them.
		if dbDeprov != nil {
			dbDeprov.DeprovisionApp(r.Context(), id)
		}
		// Tear down routes + vac-edge attachments before the cascade removes
		// the domain rows we'd need to find them.
		proxyTeardown(r.Context(), pm, id)
		if err := s.DeleteApp(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete app")
			return
		}
		// The interrupted deploy left the queue panel; nudge live subscribers so it
		// drops off without waiting for the next worker tick.
		if worker != nil {
			worker.NotifyChanged()
		}
		WriteJSON(w, http.StatusOK, map[string]int{"deleted": 1})
	}
}

// deriveSlug produces a kebab-case handle from a free-form name. Non-alnum
// runs become a single hyphen, trailing/leading hyphens are stripped.
func deriveSlug(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastHyphen := true // suppress leading hyphen
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
