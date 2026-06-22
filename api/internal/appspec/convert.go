package appspec

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// FromAppInput bundles the store rows that make up one app's portable
// configuration. Env is pre-resolved by the caller (the export handler decrypts
// non-sensitive values and omits sensitive ones) so this package never touches
// crypto. SSHPublicKey is the deploy key's public half (private is per-instance,
// never exported).
type FromAppInput struct {
	App          store.App
	Services     []store.Service
	Domains      []store.Domain
	Triggers     []store.DeployTrigger
	Env          []EnvVar
	SSHPublicKey string
}

// FromApp builds a portable Spec from an app's store rows. Output is
// deterministic (collections sorted by their stable key) so exporting the same
// app twice yields byte-for-byte identical YAML. Runtime/operational state on the
// rows (status, container IDs, counts, timestamps, cert expiry) is dropped by
// construction — only configuration crosses over.
//
// SSHPublicKey is intentionally not represented in the Spec: it is per-instance
// trust material that the destination regenerates. It rides the FromAppInput only
// so a future bundle README/format can surface it; the spec stays secret-free.
func FromApp(in FromAppInput) Spec {
	a := in.App

	src := Source{Type: a.Source}
	if src.Type == "" {
		src.Type = SourceGit
	}
	switch src.Type {
	case SourceTemplate:
		if a.TemplateID != nil {
			src.TemplateID = *a.TemplateID
		}
	default:
		src.URL = a.GitURL
		src.Branch = a.GitBranch
	}

	spec := Spec{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: a.Name, Slug: a.Slug},
		Source:     src,
		Build:      buildFromApp(a),
	}
	if a.MemLimitMB != nil {
		spec.Resources = Resources{MemLimitMB: a.MemLimitMB}
	}

	for _, s := range in.Services {
		spec.Services = append(spec.Services, Service{
			Name:         s.ServiceName,
			InternalPort: s.InternalPort,
			HealthPath:   derefStr(s.HealthPath),
			IsPrivate:    s.IsPrivate,
		})
	}
	sort.Slice(spec.Services, func(i, j int) bool { return spec.Services[i].Name < spec.Services[j].Name })

	for _, d := range in.Domains {
		typ := d.Type
		if typ == "" {
			typ = store.DomainTypeCustom
		}
		spec.Domains = append(spec.Domains, Domain{
			Hostname:   d.Hostname,
			Service:    d.ServiceName,
			Type:       typ,
			RedirectTo: d.RedirectTo,
		})
	}
	sort.Slice(spec.Domains, func(i, j int) bool { return spec.Domains[i].Hostname < spec.Domains[j].Hostname })

	for _, t := range in.Triggers {
		spec.Deploy.Triggers = append(spec.Deploy.Triggers, Trigger{Event: t.Event, Filter: t.Filter})
	}

	spec.Env = append(spec.Env, in.Env...)
	sort.Slice(spec.Env, func(i, j int) bool { return spec.Env[i].Key < spec.Env[j].Key })

	return spec
}

// buildFromApp reconstructs the Build block from the app's build_kind and the
// parsed build_config, folding the effective compose path (the config override,
// else the compose_file column) into ComposePath.
func buildFromApp(a store.App) Build {
	cfg, _ := adapter.ParseConfig(a.BuildConfig) // zero config on parse error; export must not fail on a stale row

	composePath := cfg.ComposePath
	if composePath == "" {
		composePath = a.ComposeFile
	}

	b := Build{
		Kind:               a.BuildKind,
		ComposePath:        composePath,
		AllowUnsafeCompose: cfg.AllowUnsafeCompose,
	}
	switch a.BuildKind {
	case adapter.KindDockerfile:
		if cfg.DockerfilePath != "" {
			b.Dockerfile = &DockerfileBuild{DockerfilePath: cfg.DockerfilePath}
		}
	case adapter.KindFramework:
		b.Framework = &FrameworkBuild{
			Framework:    cfg.Framework,
			BuildCommand: cfg.BuildCommand,
			StartCommand: cfg.StartCommand,
			Port:         cfg.Port,
		}
	case adapter.KindStatic:
		if cfg.StaticDir != "" || cfg.SPAFallback {
			b.Static = &StaticBuild{StaticDir: cfg.StaticDir, SPAFallback: cfg.SPAFallback}
		}
	}
	return b
}

// AppInputs is the destructured, store-ready result of importing a Spec. It maps
// directly onto store.CreateApp + the per-collection writes; the caller (import
// handler or CLI) owns persistence, secret sealing, and SSH-key (re)generation.
// BuildConfig is the canonical adapter.BuildConfig JSON (unknown fields dropped),
// matching what the create API persists.
type AppInputs struct {
	Name        string
	Slug        string
	Source      string
	GitURL      string
	GitBranch   string
	TemplateID  string
	ComposeFile string
	BuildKind   string
	BuildConfig json.RawMessage
	MemLimitMB  *int

	Services []ServiceInput
	Domains  []DomainInput
	Triggers []TriggerInput
	Env      []EnvVar
}

type ServiceInput struct {
	Name         string
	InternalPort *int
	HealthPath   *string
	IsPrivate    *bool
}

type DomainInput struct {
	Hostname    string
	ServiceName string
	Type        string
	RedirectTo  string
}

type TriggerInput struct {
	Event  string
	Filter string
}

// ToApp validates a Spec and destructures it into store-ready inputs. It is the
// inverse of FromApp on the configuration columns. Slug is derived from the name
// when omitted (matching the create handler), branch and compose path fall back
// to their defaults, and the build config is canonicalized through
// adapter.BuildConfig so it round-trips byte-for-byte with the create API.
func ToApp(s Spec) (AppInputs, error) {
	if err := s.Validate(); err != nil {
		return AppInputs{}, err
	}

	slug := strings.TrimSpace(s.Metadata.Slug)
	if slug == "" {
		slug = DeriveSlug(s.Metadata.Name)
	}

	bc := s.Build.toBuildConfig()
	rawBC, err := json.Marshal(bc)
	if err != nil {
		return AppInputs{}, err
	}

	composeFile := strings.TrimSpace(s.Build.ComposePath)
	if composeFile == "" {
		composeFile = defaultComposeFile
	}

	in := AppInputs{
		Name:        s.Metadata.Name,
		Slug:        slug,
		Source:      s.Source.Type,
		ComposeFile: composeFile,
		BuildKind:   s.Build.Kind,
		BuildConfig: rawBC,
		MemLimitMB:  s.Resources.MemLimitMB,
	}
	switch s.Source.Type {
	case SourceTemplate:
		in.TemplateID = s.Source.TemplateID
	default:
		in.GitURL = s.Source.URL
		in.GitBranch = strings.TrimSpace(s.Source.Branch)
		if in.GitBranch == "" {
			in.GitBranch = defaultBranch
		}
	}

	for _, svc := range s.Services {
		isPrivate := svc.IsPrivate
		in.Services = append(in.Services, ServiceInput{
			Name:         svc.Name,
			InternalPort: svc.InternalPort,
			HealthPath:   nilIfEmpty(svc.HealthPath),
			// Always carry the desired private state (even false) so an import
			// makes the live flag match the spec exactly.
			IsPrivate: &isPrivate,
		})
	}
	for _, d := range s.Domains {
		typ := d.Type
		if typ == "" {
			typ = store.DomainTypeCustom
		}
		host, _ := normalizeHostname(d.Hostname) // Validate already guaranteed it parses
		redirect := ""
		if d.RedirectTo != "" {
			redirect, _ = normalizeHostname(d.RedirectTo)
		}
		in.Domains = append(in.Domains, DomainInput{
			Hostname:    host,
			ServiceName: d.Service,
			Type:        typ,
			RedirectTo:  redirect,
		})
	}
	for _, t := range s.Deploy.Triggers {
		in.Triggers = append(in.Triggers, TriggerInput(t))
	}
	in.Env = append(in.Env, s.Env...)

	return in, nil
}

// toBuildConfig maps the Build block onto adapter.BuildConfig. ComposePath is
// intentionally left off: the compose path travels through the apps.compose_file
// column (AppInputs.ComposeFile), which the deploy pipeline already uses as the
// fallback when the config override is empty — so a single spec field stays
// functionally equivalent without duplicating the value into build_config.
func (b Build) toBuildConfig() adapter.BuildConfig {
	cfg := adapter.BuildConfig{AllowUnsafeCompose: b.AllowUnsafeCompose}
	if b.Dockerfile != nil {
		cfg.DockerfilePath = b.Dockerfile.DockerfilePath
	}
	if b.Framework != nil {
		cfg.Framework = b.Framework.Framework
		cfg.BuildCommand = b.Framework.BuildCommand
		cfg.StartCommand = b.Framework.StartCommand
		cfg.Port = b.Framework.Port
	}
	if b.Static != nil {
		cfg.StaticDir = b.Static.StaticDir
		cfg.SPAFallback = b.Static.SPAFallback
	}
	return cfg
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
