//go:build integration

package addon_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeWorker struct{ enqueued []string }

func (f *fakeWorker) Enqueue(id string) error { f.enqueued = append(f.enqueued, id); return nil }

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	pgC, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("vac"), postgres.WithUsername("vac"), postgres.WithPassword("vac"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
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
		t.Fatalf("migrate: %v", err)
	}
	return store.New(pool)
}

func TestInstaller_Install_Integration(t *testing.T) {
	st := setupStore(t)
	ctx := context.Background()
	box, _ := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	reg, err := addon.NewRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	worker := &fakeWorker{}
	in := addon.NewInstaller(st, box, reg, worker, nil, nil)

	res, err := in.Install(ctx, "grafana", "My Grafana", "my-grafana")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// App is template-sourced.
	app, err := st.GetApp(ctx, res.App.ID)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if app.Source != store.AppSourceTemplate {
		t.Errorf("source = %q, want template", app.Source)
	}
	if app.TemplateID == nil || *app.TemplateID != "grafana" {
		t.Errorf("template_id = %v, want grafana", app.TemplateID)
	}

	// Admin password env var was injected and a secret was generated.
	if _, err := st.GetEnvVar(ctx, app.ID, "GF_ADMIN_PASSWORD"); err != nil {
		t.Errorf("admin password env var not injected: %v", err)
	}
	if res.GeneratedSecrets["GF_ADMIN_PASSWORD"] == "" {
		t.Error("no generated admin password returned")
	}

	// A deploy was enqueued.
	if len(worker.enqueued) != 1 || worker.enqueued[0] != res.Deployment.ID {
		t.Errorf("enqueued = %v, want [%s]", worker.enqueued, res.Deployment.ID)
	}
}
