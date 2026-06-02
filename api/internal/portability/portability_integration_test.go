//go:build integration

package portability_test

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
	"github.com/vojir-mikulas/vac/api/internal/appspec"
	vaccrypto "github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/portability"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func setup(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	pgC, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("vac"),
		postgres.WithUsername("vac"),
		postgres.WithPassword("vac"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker / postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(ctx) })

	url, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return store.New(pool)
}

func newBox(t *testing.T) *vaccrypto.Box {
	t.Helper()
	key := make([]byte, vaccrypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := vaccrypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return box
}

func intp(i int) *int { return &i }

func sampleSpec() appspec.Spec {
	return appspec.Spec{
		APIVersion: appspec.APIVersion,
		Kind:       appspec.Kind,
		Metadata:   appspec.Metadata{Name: "My Blog"}, // slug derived → my-blog
		Source:     appspec.Source{Type: appspec.SourceGit, URL: "git@github.com:me/blog.git", Branch: "main"},
		Build:      appspec.Build{Kind: adapter.KindCompose, ComposePath: "compose.yaml"},
		Resources:  appspec.Resources{MemLimitMB: intp(512)},
		Services:   []appspec.Service{{Name: "web", InternalPort: intp(3000), HealthPath: "/healthz"}},
		Deploy: appspec.Deploy{Triggers: []appspec.Trigger{
			{Event: "push", Filter: "main"},
			{Event: "manual"},
		}},
		Domains: []appspec.Domain{{Hostname: "blog.example.com", Service: "web", Type: store.DomainTypeCustom}},
		Env: []appspec.EnvVar{
			{Key: "NODE_ENV", Value: "production"},
			{Key: "DATABASE_URL", Sensitive: true}, // value re-pasted on the far side
		},
	}
}

func TestImportThenExport_RoundTrip(t *testing.T) {
	s := setup(t)
	box := newBox(t)
	ctx := context.Background()

	res, err := portability.Import(ctx, s, box, sampleSpec())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !res.Created || res.Slug != "my-blog" {
		t.Fatalf("import result: %+v", res)
	}
	if res.Services != 1 || res.Domains != 1 || res.Triggers != 2 || res.EnvVars != 2 {
		t.Fatalf("import counts: %+v", res)
	}
	if len(res.SecretsNeeded) != 1 || res.SecretsNeeded[0] != "DATABASE_URL" {
		t.Fatalf("secrets needed: %v", res.SecretsNeeded)
	}

	// Verify the persisted state directly.
	app, err := s.GetAppBySlug(ctx, "my-blog")
	if err != nil {
		t.Fatalf("GetAppBySlug: %v", err)
	}
	if app.GitURL != "git@github.com:me/blog.git" || app.MemLimitMB == nil || *app.MemLimitMB != 512 {
		t.Errorf("app row: %+v", app)
	}
	svcs, _ := s.ListServicesForApp(ctx, app.ID)
	if len(svcs) != 1 || svcs[0].ServiceName != "web" || svcs[0].InternalPort == nil || *svcs[0].InternalPort != 3000 {
		t.Fatalf("services: %+v", svcs)
	}
	if svcs[0].HealthPath == nil || *svcs[0].HealthPath != "/healthz" {
		t.Errorf("health path not set: %+v", svcs[0])
	}
	envRows, _ := s.ListEnvVarsForApp(ctx, app.ID)
	if len(envRows) != 2 {
		t.Fatalf("env rows: %d", len(envRows))
	}
	for _, v := range envRows {
		plain, err := box.Open(v.Value)
		if err != nil {
			t.Fatalf("env %q not sealed/openable: %v", v.Key, err)
		}
		switch v.Key {
		case "NODE_ENV":
			if string(plain) != "production" || v.Sensitive {
				t.Errorf("NODE_ENV: %q sensitive=%v", plain, v.Sensitive)
			}
		case "DATABASE_URL":
			if string(plain) != "" || !v.Sensitive {
				t.Errorf("DATABASE_URL placeholder: %q sensitive=%v", plain, v.Sensitive)
			}
		}
	}

	// Export and check the spec mirrors what we put in (secrets omitted).
	spec, err := portability.Export(ctx, s, box, app.ID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("exported spec invalid: %v", err)
	}
	if spec.Metadata.Slug != "my-blog" || spec.Metadata.Name != "My Blog" {
		t.Errorf("metadata: %+v", spec.Metadata)
	}
	if spec.Resources.MemLimitMB == nil || *spec.Resources.MemLimitMB != 512 {
		t.Errorf("mem limit: %v", spec.Resources.MemLimitMB)
	}
	if len(spec.Services) != 1 || spec.Services[0].HealthPath != "/healthz" {
		t.Errorf("services: %+v", spec.Services)
	}
	if len(spec.Domains) != 1 || spec.Domains[0].Hostname != "blog.example.com" || spec.Domains[0].Service != "web" {
		t.Errorf("domains: %+v", spec.Domains)
	}
	// Env sorted by key: DATABASE_URL (sensitive, no value) then NODE_ENV.
	if len(spec.Env) != 2 || spec.Env[0].Key != "DATABASE_URL" || spec.Env[0].Value != "" || !spec.Env[0].Sensitive {
		t.Errorf("sensitive env not omitted: %+v", spec.Env)
	}
	if spec.Env[1].Key != "NODE_ENV" || spec.Env[1].Value != "production" {
		t.Errorf("non-sensitive env: %+v", spec.Env[1])
	}
}

func TestImport_IdempotentOnSlug(t *testing.T) {
	s := setup(t)
	box := newBox(t)
	ctx := context.Background()

	if _, err := portability.Import(ctx, s, box, sampleSpec()); err != nil {
		t.Fatalf("first import: %v", err)
	}
	app, _ := s.GetAppBySlug(ctx, "my-blog")

	// Mark the service running, as a deploy would, to prove re-import doesn't
	// clobber runtime status.
	if err := s.UpdateServiceStatus(ctx, app.ID, "web", "running", nil); err != nil {
		t.Fatalf("UpdateServiceStatus: %v", err)
	}

	res, err := portability.Import(ctx, s, box, sampleSpec())
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Created {
		t.Errorf("re-import should update in place, not create")
	}
	if res.AppID != app.ID {
		t.Errorf("re-import created a new app: %s vs %s", res.AppID, app.ID)
	}

	// No duplication: still one app, one service, one domain, two triggers.
	if apps, _ := s.ListApps(ctx); len(apps) != 1 {
		t.Errorf("apps after re-import: %d", len(apps))
	}
	if doms, _ := s.ListDomainsByApp(ctx, app.ID); len(doms) != 1 {
		t.Errorf("domains after re-import: %d", len(doms))
	}
	if trigs, _ := s.ListDeployTriggers(ctx, app.ID); len(trigs) != 2 {
		t.Errorf("triggers after re-import: %d", len(trigs))
	}
	svcs, _ := s.ListServicesForApp(ctx, app.ID)
	if len(svcs) != 1 || svcs[0].Status != "running" {
		t.Errorf("re-import clobbered service status: %+v", svcs)
	}
}

func TestImport_RequiresMasterKeyForEnv(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	// No box, but the spec carries env → must refuse rather than store unsealed.
	if _, err := portability.Import(ctx, s, nil, sampleSpec()); err != portability.ErrMasterKeyRequired {
		t.Fatalf("want ErrMasterKeyRequired, got %v", err)
	}

	// A spec with no env imports fine without a key.
	spec := sampleSpec()
	spec.Env = nil
	if _, err := portability.Import(ctx, s, nil, spec); err != nil {
		t.Fatalf("import without env should not need a key: %v", err)
	}
}

func TestImport_RejectsInvalidSpec(t *testing.T) {
	s := setup(t)
	box := newBox(t)
	ctx := context.Background()

	spec := sampleSpec()
	spec.Source.URL = "" // git source without a URL
	_, err := portability.Import(ctx, s, box, spec)
	var invalid portability.InvalidSpecError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidSpecError, got %v", err)
	}
}
