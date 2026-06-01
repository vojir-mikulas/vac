package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
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
}

func toAppDTO(a store.App) appDTO {
	bc := a.BuildConfig
	if len(bc) == 0 {
		bc = json.RawMessage("{}")
	}
	return appDTO{
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
	}
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
func CreateApp(s *store.Store) http.HandlerFunc {
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
		WriteJSON(w, http.StatusCreated, toAppDTO(a))
	}
}

func ListApps(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListApps(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list apps")
			return
		}
		out := make([]appDTO, 0, len(rows))
		for _, a := range rows {
			out = append(out, toAppDTO(a))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

func GetApp(s *store.Store) http.HandlerFunc {
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
		WriteJSON(w, http.StatusOK, toAppDTO(a))
	}
}

// UpdateApp applies a partial JSON patch. Slug is read-only here — once
// chosen, the slug is the app's stable handle.
func UpdateApp(s *store.Store) http.HandlerFunc {
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

		a, err := s.UpdateApp(r.Context(), id, req.Name, req.GitURL, req.GitBranch, req.ComposeFile, req.BuildKind, buildConfig, req.MemLimitMB)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update app")
			return
		}
		WriteJSON(w, http.StatusOK, toAppDTO(a))
	}
}

func DeleteApp(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
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
