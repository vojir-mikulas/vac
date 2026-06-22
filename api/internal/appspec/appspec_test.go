package appspec_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
	"github.com/vojir-mikulas/vac/api/internal/appspec"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func intp(i int) *int       { return &i }
func strp(s string) *string { return &s }
func bc(t *testing.T, c adapter.BuildConfig) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal build config: %v", err)
	}
	return raw
}

// fullApp is a git app exercising every configuration surface the spec carries.
func fullApp(t *testing.T) appspec.FromAppInput {
	t.Helper()
	return appspec.FromAppInput{
		App: store.App{
			ID:          "app-uuid",
			Name:        "My Blog",
			Slug:        "my-blog",
			GitURL:      "git@github.com:me/blog.git",
			GitBranch:   "main",
			ComposeFile: "compose.yaml",
			BuildKind:   adapter.KindCompose,
			BuildConfig: bc(t, adapter.BuildConfig{}),
			Status:      "running", // runtime state — must NOT survive into the spec
			MemLimitMB:  intp(512),
			Source:      store.AppSourceGit,
		},
		Services: []store.Service{
			{ServiceName: "web", InternalPort: intp(3000), HealthPath: strp("/healthz"), Status: "running"},
			{ServiceName: "api", InternalPort: intp(8080), Status: "running"},
		},
		Domains: []store.Domain{
			{Hostname: "blog.example.com", ServiceName: "web", Type: store.DomainTypeCustom},
			{Hostname: "www.example.com", RedirectTo: "blog.example.com", Type: store.DomainTypeCustom},
		},
		Triggers: []store.DeployTrigger{
			{Event: store.TriggerEventPush, Filter: "main"},
			{Event: store.TriggerEventManual},
		},
		Env: []appspec.EnvVar{
			{Key: "NODE_ENV", Value: "production", Sensitive: false},
			{Key: "DATABASE_URL", Sensitive: true}, // value omitted by the caller
		},
		SSHPublicKey: "ssh-ed25519 AAAA... deploy@vac",
	}
}

func TestFromApp_DropsRuntimeState(t *testing.T) {
	t.Parallel()
	spec := appspec.FromApp(fullApp(t))

	if spec.APIVersion != appspec.APIVersion || spec.Kind != appspec.Kind {
		t.Fatalf("bad header: %q %q", spec.APIVersion, spec.Kind)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("exported spec failed validation: %v", err)
	}
	// Status is runtime state; it must not appear anywhere in the serialized spec.
	out, err := appspec.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); contains(got, "running") {
		t.Errorf("runtime status leaked into spec:\n%s", got)
	}
}

func TestFromApp_Deterministic(t *testing.T) {
	t.Parallel()
	// Same app, collections in a different input order → identical output.
	in := fullApp(t)
	shuffled := fullApp(t)
	shuffled.Services[0], shuffled.Services[1] = shuffled.Services[1], shuffled.Services[0]
	shuffled.Domains[0], shuffled.Domains[1] = shuffled.Domains[1], shuffled.Domains[0]
	shuffled.Env[0], shuffled.Env[1] = shuffled.Env[1], shuffled.Env[0]

	a, err := appspec.Marshal(appspec.FromApp(in))
	if err != nil {
		t.Fatal(err)
	}
	b, err := appspec.Marshal(appspec.FromApp(shuffled))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("export not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

func TestRoundTrip_StoreToSpecToInputs(t *testing.T) {
	t.Parallel()
	in := fullApp(t)
	spec := appspec.FromApp(in)

	// Through the wire: marshal → unmarshal must preserve the spec exactly.
	data, err := appspec.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec2, err := appspec.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec, spec2) {
		t.Fatalf("YAML round-trip changed the spec:\nwant %#v\ngot  %#v", spec, spec2)
	}

	got, err := appspec.ToApp(spec2)
	if err != nil {
		t.Fatalf("ToApp: %v", err)
	}

	app := in.App
	if got.Name != app.Name || got.Slug != app.Slug {
		t.Errorf("metadata mismatch: %q/%q", got.Name, got.Slug)
	}
	if got.GitURL != app.GitURL || got.GitBranch != app.GitBranch {
		t.Errorf("source mismatch: %q@%q", got.GitURL, got.GitBranch)
	}
	if got.Source != store.AppSourceGit {
		t.Errorf("source type: %q", got.Source)
	}
	if got.ComposeFile != app.ComposeFile {
		t.Errorf("compose file: %q", got.ComposeFile)
	}
	if got.BuildKind != app.BuildKind {
		t.Errorf("build kind: %q", got.BuildKind)
	}
	if string(got.BuildConfig) != string(app.BuildConfig) {
		t.Errorf("build config: %s want %s", got.BuildConfig, app.BuildConfig)
	}
	if got.MemLimitMB == nil || *got.MemLimitMB != 512 {
		t.Errorf("mem limit: %v", got.MemLimitMB)
	}
	if len(got.Services) != 2 || got.Services[0].Name != "api" || got.Services[1].Name != "web" {
		t.Fatalf("services: %#v", got.Services)
	}
	if got.Services[1].HealthPath == nil || *got.Services[1].HealthPath != "/healthz" {
		t.Errorf("web health path: %v", got.Services[1].HealthPath)
	}
	if len(got.Domains) != 2 {
		t.Fatalf("domains: %#v", got.Domains)
	}
	if got.Domains[1].Hostname != "www.example.com" || got.Domains[1].RedirectTo != "blog.example.com" || got.Domains[1].ServiceName != "" {
		t.Errorf("redirect domain: %#v", got.Domains[1])
	}
	if len(got.Triggers) != 2 || got.Triggers[0].Event != "push" || got.Triggers[0].Filter != "main" {
		t.Errorf("triggers: %#v", got.Triggers)
	}
	if len(got.Env) != 2 || got.Env[0].Key != "DATABASE_URL" || got.Env[1].Key != "NODE_ENV" {
		t.Fatalf("env: %#v", got.Env)
	}
	if got.Env[1].Value != "production" {
		t.Errorf("non-sensitive value dropped: %#v", got.Env[1])
	}
	if got.Env[0].Value != "" || !got.Env[0].Sensitive {
		t.Errorf("sensitive value should stay omitted: %#v", got.Env[0])
	}
}

// A service marked private must keep that flag across export → YAML → import, so
// re-importing an app can't silently re-publish an internal-only service.
func TestRoundTrip_PreservesIsPrivate(t *testing.T) {
	t.Parallel()
	in := appspec.FromAppInput{
		App: store.App{
			Name:        "intake",
			Slug:        "intake",
			GitURL:      "git@github.com:me/intake.git",
			GitBranch:   "main",
			ComposeFile: "compose.yaml",
			BuildKind:   adapter.KindCompose,
			BuildConfig: bc(t, adapter.BuildConfig{}),
			Source:      store.AppSourceGit,
		},
		Services: []store.Service{
			{ServiceName: "api", InternalPort: intp(8080)},
			{ServiceName: "meilisearch", InternalPort: intp(7700), IsPrivate: true},
		},
	}
	spec := appspec.FromApp(in)

	data, err := appspec.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec2, err := appspec.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := appspec.ToApp(spec2)
	if err != nil {
		t.Fatalf("ToApp: %v", err)
	}

	// Services are sorted by name: api, meilisearch.
	byName := map[string]appspec.ServiceInput{}
	for _, s := range got.Services {
		byName[s.Name] = s
	}
	if s := byName["meilisearch"]; s.IsPrivate == nil || !*s.IsPrivate {
		t.Errorf("meilisearch should round-trip private, got %v", s.IsPrivate)
	}
	if s := byName["api"]; s.IsPrivate == nil || *s.IsPrivate {
		t.Errorf("api should round-trip public (is_private=false), got %v", s.IsPrivate)
	}
}

func TestBuildKinds_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind string
		cfg  adapter.BuildConfig
	}{
		{"framework", adapter.KindFramework, adapter.BuildConfig{Framework: "react", BuildCommand: "pnpm build", StartCommand: "node server.js", Port: 3000}},
		{"dockerfile", adapter.KindDockerfile, adapter.BuildConfig{DockerfilePath: "docker/Dockerfile"}},
		{"static", adapter.KindStatic, adapter.BuildConfig{StaticDir: "dist", SPAFallback: true}},
		{"compose-unsafe", adapter.KindCompose, adapter.BuildConfig{AllowUnsafeCompose: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := store.App{
				Name: "x", Slug: "x", GitURL: "git@h:r.git", GitBranch: "main",
				ComposeFile: "compose.yaml", BuildKind: c.kind,
				BuildConfig: bc(t, c.cfg), Source: store.AppSourceGit,
			}
			spec := appspec.FromApp(appspec.FromAppInput{App: app})
			if err := spec.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			got, err := appspec.ToApp(spec)
			if err != nil {
				t.Fatalf("ToApp: %v", err)
			}
			if got.BuildKind != c.kind {
				t.Errorf("kind: %q", got.BuildKind)
			}
			if string(got.BuildConfig) != string(app.BuildConfig) {
				t.Errorf("build config: %s want %s", got.BuildConfig, app.BuildConfig)
			}
		})
	}
}

// The compose path may be set as a build_config override OR the compose_file
// column; the spec collapses both into build.composePath. A round-trip must keep
// the *effective* path the deploy pipeline would use (override, else column).
func TestComposePath_OverrideFoldsToColumn(t *testing.T) {
	t.Parallel()
	app := store.App{
		Name: "x", Slug: "x", GitURL: "git@h:r.git", GitBranch: "main",
		ComposeFile: "compose.yaml", // column fallback
		BuildKind:   adapter.KindCompose,
		BuildConfig: bc(t, adapter.BuildConfig{ComposePath: "deploy/prod.yaml"}), // override wins
		Source:      store.AppSourceGit,
	}
	spec := appspec.FromApp(appspec.FromAppInput{App: app})
	if spec.Build.ComposePath != "deploy/prod.yaml" {
		t.Fatalf("effective compose path not captured: %q", spec.Build.ComposePath)
	}
	got, err := appspec.ToApp(spec)
	if err != nil {
		t.Fatal(err)
	}
	// On import the effective path lands in the column (deploy uses it as the
	// fallback when the config override is empty) — functionally identical.
	if got.ComposeFile != "deploy/prod.yaml" {
		t.Errorf("compose file: %q", got.ComposeFile)
	}
	var cfg adapter.BuildConfig
	if err := json.Unmarshal(got.BuildConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ComposePath != "" {
		t.Errorf("compose path should not be duplicated into build_config: %q", cfg.ComposePath)
	}
}

func TestToApp_DerivesSlugAndDefaults(t *testing.T) {
	t.Parallel()
	spec := appspec.Spec{
		APIVersion: appspec.APIVersion,
		Kind:       appspec.Kind,
		Metadata:   appspec.Metadata{Name: "My Cool App!"},                      // no slug
		Source:     appspec.Source{Type: appspec.SourceGit, URL: "git@h:r.git"}, // no branch
		Build:      appspec.Build{Kind: adapter.KindCompose},                    // no compose path
	}
	got, err := appspec.ToApp(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "my-cool-app" {
		t.Errorf("slug: %q", got.Slug)
	}
	if got.GitBranch != "main" {
		t.Errorf("branch default: %q", got.GitBranch)
	}
	if got.ComposeFile != "compose.yaml" {
		t.Errorf("compose default: %q", got.ComposeFile)
	}
}

func TestTemplateSource_RoundTrip(t *testing.T) {
	t.Parallel()
	app := store.App{
		Name: "Grafana", Slug: "grafana", ComposeFile: "compose.yaml",
		BuildKind: adapter.KindCompose, BuildConfig: bc(t, adapter.BuildConfig{}),
		Source: store.AppSourceTemplate, TemplateID: strp("grafana"),
	}
	spec := appspec.FromApp(appspec.FromAppInput{App: app})
	if spec.Source.Type != appspec.SourceTemplate || spec.Source.TemplateID != "grafana" {
		t.Fatalf("source: %#v", spec.Source)
	}
	if spec.Source.URL != "" {
		t.Errorf("template app must not carry a git url: %q", spec.Source.URL)
	}
	got, err := appspec.ToApp(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != store.AppSourceTemplate || got.TemplateID != "grafana" || got.GitURL != "" {
		t.Errorf("template import: %#v", got)
	}
}

func TestValidate_Rejects(t *testing.T) {
	t.Parallel()
	base := func() appspec.Spec {
		return appspec.Spec{
			APIVersion: appspec.APIVersion, Kind: appspec.Kind,
			Metadata: appspec.Metadata{Name: "x"},
			Source:   appspec.Source{Type: appspec.SourceGit, URL: "git@h:r.git"},
			Build:    appspec.Build{Kind: adapter.KindCompose},
		}
	}
	cases := map[string]func(*appspec.Spec){
		"bad apiVersion":    func(s *appspec.Spec) { s.APIVersion = "vac/v2" },
		"bad kind":          func(s *appspec.Spec) { s.Kind = "Stack" },
		"empty name":        func(s *appspec.Spec) { s.Metadata.Name = "  " },
		"git without url":   func(s *appspec.Spec) { s.Source = appspec.Source{Type: appspec.SourceGit} },
		"unknown source":    func(s *appspec.Spec) { s.Source.Type = "svn" },
		"bad build kind":    func(s *appspec.Spec) { s.Build.Kind = "make" },
		"framework no name": func(s *appspec.Spec) { s.Build = appspec.Build{Kind: adapter.KindFramework} },
		"dup hostname": func(s *appspec.Spec) {
			s.Domains = []appspec.Domain{{Hostname: "a.example.com"}, {Hostname: "a.example.com"}}
		},
		"bad hostname":       func(s *appspec.Spec) { s.Domains = []appspec.Domain{{Hostname: "https://a.com/x"}} },
		"undeclared service": func(s *appspec.Spec) { s.Domains = []appspec.Domain{{Hostname: "a.example.com", Service: "ghost"}} },
		"self redirect": func(s *appspec.Spec) {
			s.Domains = []appspec.Domain{{Hostname: "a.example.com", RedirectTo: "A.example.com."}}
		},
		"dup env key":       func(s *appspec.Spec) { s.Env = []appspec.EnvVar{{Key: "A"}, {Key: "A"}} },
		"bad env key":       func(s *appspec.Spec) { s.Env = []appspec.EnvVar{{Key: "1BAD"}} },
		"bad trigger event": func(s *appspec.Spec) { s.Deploy.Triggers = []appspec.Trigger{{Event: "merge"}} },
		"dup service":       func(s *appspec.Spec) { s.Services = []appspec.Service{{Name: "w"}, {Name: "w"}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := base()
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Errorf("expected validation error for %q", name)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
