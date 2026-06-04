// Package appspec is the single source of truth for VAC's portable app
// specification (`vac.app.yaml`, apiVersion vac/v1). It defines the declarative
// types, their YAML (de)serialization, validation, and the lossless translation
// to/from the store models that back the import (on-ramp) and export (exit-ramp)
// flows (plan 18).
//
// The spec deliberately carries only *configuration* — the surface VAC adds
// around a user's git repo + compose file. Operational/runtime state (status,
// container IDs, restart/OOM counts, cert expiry, timestamps) is excluded: it is
// re-derived on the destination, not transported.
//
// Secret material is never serialized here. Env entries carry their key and
// sensitivity but values are the caller's concern (omitted for sensitive keys,
// re-pasted on the far side); the deploy SSH private key is per-instance and only
// its public half is ever exported. See the plan's "Secrets handling" section.
package appspec

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
)

// maxSlugLen mirrors the create handler's slug cap.
const maxSlugLen = 63

// gitURLRe / gitRefRe / slugRe replicate the HTTP create handler's validation
// (handler.gitURLRe etc.) so the import on-ramp (POST /import and `vac-api
// apply`) enforces the exact same shape the create API does. Without this the
// import path would reach gitcli.Clone with a fully unvalidated URL — letting an
// `ext::sh -c …` transport URL or a `metadata.slug: ../../etc` traversal through.
// Kept replicated (not imported) so appspec stays free of the HTTP handler
// dependency, the same trade-off as normalizeHostname above. Keep in sync with
// handler/apps.go.
var (
	gitURLRe = regexp.MustCompile(`^(?:https?://\S+|git@[^\s:]+:\S+|ssh://git@\S+/\S+)$`)
	gitRefRe = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]*$`)
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// APIVersion / Kind gate the spec for forward-compat. v1 evolves by additive
// fields only; a reader that sees a future apiVersion it doesn't recognize
// should refuse rather than silently mis-import.
const (
	APIVersion = "vac/v1"
	Kind       = "App"
)

// Source kinds mirror store.AppSource{Git,Template}.
const (
	SourceGit      = "git"
	SourceTemplate = "template"
)

// Defaults applied on import when a field is omitted. Kept local to avoid
// importing the HTTP handler package (which would invert the dependency).
const (
	defaultBranch      = "main"
	defaultComposeFile = "compose.yaml"
)

// Spec is one portable VAC app. It is the lingua franca for both import and
// export; FromApp/ToApp bridge it to the store models.
type Spec struct {
	APIVersion string    `yaml:"apiVersion" json:"apiVersion"`
	Kind       string    `yaml:"kind"       json:"kind"`
	Metadata   Metadata  `yaml:"metadata"   json:"metadata"`
	Source     Source    `yaml:"source"     json:"source"`
	Build      Build     `yaml:"build"      json:"build"`
	Resources  Resources `yaml:"resources,omitempty" json:"resources,omitempty"`
	Services   []Service `yaml:"services,omitempty"  json:"services,omitempty"`
	Deploy     Deploy    `yaml:"deploy,omitempty"    json:"deploy,omitempty"`
	Domains    []Domain  `yaml:"domains,omitempty"   json:"domains,omitempty"`
	Env        []EnvVar  `yaml:"env,omitempty"       json:"env,omitempty"`
}

// Metadata identifies the app. Slug is optional on import (derived from Name
// when absent) and is the stable URL/routing handle once set.
type Metadata struct {
	Name string `yaml:"name"           json:"name"`
	Slug string `yaml:"slug,omitempty" json:"slug,omitempty"`
}

// Source is where the app's code comes from. type=git clones URL@Branch;
// type=template materializes an embedded add-on (URL empty, TemplateID set).
type Source struct {
	Type       string `yaml:"type"                 json:"type"`
	URL        string `yaml:"url,omitempty"        json:"url,omitempty"`
	Branch     string `yaml:"branch,omitempty"     json:"branch,omitempty"`
	TemplateID string `yaml:"templateId,omitempty" json:"templateId,omitempty"`
}

// Build selects the deploy adapter and its knobs. Kind + the matching sub-block
// map 1:1 to apps.build_kind / apps.build_config (adapter.BuildConfig).
// ComposePath is the compose file path; on import it lands in the apps.compose_file
// column, which the deploy pipeline uses as the compose-path fallback.
type Build struct {
	Kind               string           `yaml:"kind"                         json:"kind"`
	ComposePath        string           `yaml:"composePath,omitempty"        json:"composePath,omitempty"`
	AllowUnsafeCompose bool             `yaml:"allowUnsafeCompose,omitempty" json:"allowUnsafeCompose,omitempty"`
	Dockerfile         *DockerfileBuild `yaml:"dockerfile,omitempty"         json:"dockerfile,omitempty"`
	Framework          *FrameworkBuild  `yaml:"framework,omitempty"          json:"framework,omitempty"`
	Static             *StaticBuild     `yaml:"static,omitempty"             json:"static,omitempty"`
}

type DockerfileBuild struct {
	DockerfilePath string `yaml:"dockerfilePath,omitempty" json:"dockerfilePath,omitempty"`
}

type FrameworkBuild struct {
	Framework    string `yaml:"framework"              json:"framework"`
	BuildCommand string `yaml:"buildCommand,omitempty" json:"buildCommand,omitempty"`
	StartCommand string `yaml:"startCommand,omitempty" json:"startCommand,omitempty"`
	Port         int    `yaml:"port,omitempty"         json:"port,omitempty"`
}

type StaticBuild struct {
	StaticDir   string `yaml:"staticDir,omitempty"   json:"staticDir,omitempty"`
	SPAFallback bool   `yaml:"spaFallback,omitempty" json:"spaFallback,omitempty"`
}

// Resources is the per-app budget. MemLimitMB nil/omitted = unlimited.
type Resources struct {
	MemLimitMB *int `yaml:"memLimitMB,omitempty" json:"memLimitMB,omitempty"`
}

// Service is the routable surface VAC tracks for an app. internalPort is the
// container port Caddy dials over vac-edge; healthPath is the operator-set
// health endpoint. (Service rows are otherwise discovered from compose at deploy
// time — exposed_port and runtime state are not part of the portable spec.)
type Service struct {
	Name         string `yaml:"name"                   json:"name"`
	InternalPort *int   `yaml:"internalPort,omitempty" json:"internalPort,omitempty"`
	HealthPath   string `yaml:"healthPath,omitempty"   json:"healthPath,omitempty"`
}

// Deploy holds push-to-deploy configuration. Triggers map to deploy_triggers.
type Deploy struct {
	Triggers []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`
}

type Trigger struct {
	Event  string `yaml:"event"            json:"event"`
	Filter string `yaml:"filter,omitempty" json:"filter,omitempty"`
}

// Domain is one hostname routed to a service, or a redirect to another host.
// Service is empty for a pure redirect (redirectTo set) or an unassigned domain.
type Domain struct {
	Hostname   string `yaml:"hostname"             json:"hostname"`
	Service    string `yaml:"service,omitempty"    json:"service,omitempty"`
	Type       string `yaml:"type,omitempty"       json:"type,omitempty"`
	RedirectTo string `yaml:"redirectTo,omitempty" json:"redirectTo,omitempty"`
}

// EnvVar is one environment entry. Value is present for non-sensitive keys and
// omitted for sensitive ones (re-pasted on the far side); it is never the sealed
// ciphertext. See the package doc and the plan's secrets handling.
type EnvVar struct {
	Key       string `yaml:"key"             json:"key"`
	Value     string `yaml:"value,omitempty" json:"value,omitempty"`
	Sensitive bool   `yaml:"sensitive,omitempty" json:"sensitive,omitempty"`
}

// Marshal renders the spec to canonical YAML bytes.
func Marshal(s Spec) ([]byte, error) {
	return yaml.Marshal(s)
}

// Unmarshal parses spec YAML. Decoding is lenient on unknown fields so a v1
// reader can ingest a forward-compatible (additively-extended) document; the
// apiVersion/kind gate in Validate is what guards against genuinely foreign input.
func Unmarshal(data []byte) (Spec, error) {
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("appspec: parse: %w", err)
	}
	return s, nil
}

// Validate checks a spec is internally consistent and importable. It reuses
// adapter.Validate for the build block, so an inconsistent (kind, config) pair is
// rejected here exactly as the create API rejects it. It does not touch the
// filesystem or the network — clone-time failure is the real validator for the
// git URL, mirroring the create handler.
func (s Spec) Validate() error {
	if s.APIVersion != APIVersion {
		return fmt.Errorf("appspec: unsupported apiVersion %q (want %q)", s.APIVersion, APIVersion)
	}
	if s.Kind != Kind {
		return fmt.Errorf("appspec: unsupported kind %q (want %q)", s.Kind, Kind)
	}
	if strings.TrimSpace(s.Metadata.Name) == "" {
		return fmt.Errorf("appspec: metadata.name is required")
	}
	// An explicit slug is operator-controlled and flows into a filesystem path
	// (filepath.Join(WorkDir, slug, "repo")) at deploy time, so it must be a
	// strict kebab-case handle — never a traversal like "../../root/.ssh". A
	// derived slug (DeriveSlug, when slug is omitted) is safe by construction.
	if slug := strings.TrimSpace(s.Metadata.Slug); slug != "" {
		if len(slug) > maxSlugLen || !slugRe.MatchString(slug) {
			return fmt.Errorf("appspec: metadata.slug %q must match %s (max %d chars)", slug, slugRe.String(), maxSlugLen)
		}
	}
	switch s.Source.Type {
	case SourceGit:
		url := strings.TrimSpace(s.Source.URL)
		if url == "" {
			return fmt.Errorf("appspec: source.url is required for source.type=git")
		}
		// The URL is passed to `git clone` as a positional argument; validate it
		// to the same allowlist the create API enforces so a hostile transport
		// (e.g. `ext::sh -c id`) can't reach gitcli through the import path.
		if !gitURLRe.MatchString(url) {
			return fmt.Errorf("appspec: source.url %q is not a valid git URL", url)
		}
		if branch := strings.TrimSpace(s.Source.Branch); branch != "" && !gitRefRe.MatchString(branch) {
			return fmt.Errorf("appspec: source.branch %q must match %s", branch, gitRefRe.String())
		}
	case SourceTemplate:
		if strings.TrimSpace(s.Source.TemplateID) == "" {
			return fmt.Errorf("appspec: source.templateId is required for source.type=template")
		}
	case "":
		return fmt.Errorf("appspec: source.type is required (git|template)")
	default:
		return fmt.Errorf("appspec: unknown source.type %q (want git|template)", s.Source.Type)
	}
	if !validBuildKind(s.Build.Kind) {
		return fmt.Errorf("appspec: build.kind must be one of auto, compose, dockerfile, framework, static")
	}
	if err := adapter.Validate(s.Build.Kind, s.Build.toBuildConfig()); err != nil {
		return fmt.Errorf("appspec: %w", err)
	}
	seenSvc := map[string]struct{}{}
	for _, svc := range s.Services {
		if strings.TrimSpace(svc.Name) == "" {
			return fmt.Errorf("appspec: service name is required")
		}
		if _, dup := seenSvc[svc.Name]; dup {
			return fmt.Errorf("appspec: duplicate service %q", svc.Name)
		}
		seenSvc[svc.Name] = struct{}{}
	}
	seenHost := map[string]struct{}{}
	for _, d := range s.Domains {
		host, err := normalizeHostname(d.Hostname)
		if err != nil {
			return fmt.Errorf("appspec: domain %q: %w", d.Hostname, err)
		}
		if _, dup := seenHost[host]; dup {
			return fmt.Errorf("appspec: duplicate domain %q", host)
		}
		seenHost[host] = struct{}{}
		// A domain bound to a service requires that service to be declared, since
		// import pre-creates the service rows the composite FK points at.
		if d.Service != "" {
			if _, ok := seenSvc[d.Service]; !ok {
				return fmt.Errorf("appspec: domain %q references undeclared service %q", host, d.Service)
			}
		}
		if d.RedirectTo != "" {
			target, err := normalizeHostname(d.RedirectTo)
			if err != nil {
				return fmt.Errorf("appspec: domain %q redirectTo: %w", host, err)
			}
			if target == host {
				return fmt.Errorf("appspec: domain %q cannot redirect to itself", host)
			}
		}
	}
	for _, t := range s.Deploy.Triggers {
		switch t.Event {
		case "push", "tag", "manual":
		default:
			return fmt.Errorf("appspec: unknown trigger event %q (want push|tag|manual)", t.Event)
		}
	}
	seenKey := map[string]struct{}{}
	for _, e := range s.Env {
		if !validEnvKey(e.Key) {
			return fmt.Errorf("appspec: invalid env key %q", e.Key)
		}
		if _, dup := seenKey[e.Key]; dup {
			return fmt.Errorf("appspec: duplicate env key %q", e.Key)
		}
		seenKey[e.Key] = struct{}{}
		// Reject control characters that would corrupt the rendered .env file or
		// smuggle a second VAR=value pair — the same boundary the env API enforces.
		if strings.ContainsAny(e.Value, "\n\r\x00") {
			return fmt.Errorf("appspec: env value for %q contains a newline or NUL", e.Key)
		}
	}
	return nil
}

func validBuildKind(kind string) bool {
	switch kind {
	case adapter.KindAuto, adapter.KindCompose, adapter.KindDockerfile, adapter.KindFramework, adapter.KindStatic:
		return true
	default:
		return false
	}
}

// validEnvKey mirrors the handler's POSIX-style rule: first char a letter or
// underscore, the rest letters/digits/underscore.
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		isAlpha := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isAlpha {
			return false
		}
		if !isAlpha && !isDigit {
			return false
		}
	}
	return true
}

// normalizeHostname lower-cases and validates a hostname, rejecting schemes,
// ports, paths, wildcards, and non-FQDNs. It mirrors proxy.NormalizeHostname so
// import accepts exactly what the domain API accepts — replicated here to keep
// appspec free of the heavyweight proxy dependency (which would bloat the CLI
// binary). Keep the two in sync.
func normalizeHostname(raw string) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	switch {
	case h == "":
		return "", fmt.Errorf("hostname is required")
	case strings.ContainsAny(h, " \t\n\r"):
		return "", fmt.Errorf("hostname contains whitespace")
	case strings.Contains(h, "*"):
		return "", fmt.Errorf("wildcards not allowed")
	case strings.ContainsAny(h, "/:?#@"):
		return "", fmt.Errorf("must not contain a scheme, port, or path")
	case !strings.Contains(h, "."):
		return "", fmt.Errorf("must be a fully-qualified domain")
	case len(h) > 253:
		return "", fmt.Errorf("hostname too long")
	}
	return h, nil
}

// DeriveSlug produces a kebab-case handle from a free-form name — the same
// transformation the create handler applies, replicated here so the CLI import
// path (which never touches the HTTP layer) derives identical slugs.
func DeriveSlug(name string) string {
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
