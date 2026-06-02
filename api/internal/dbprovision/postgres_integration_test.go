//go:build integration

package dbprovision_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
)

func setupPool(t *testing.T) *pgxpool.Pool {
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
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPostgresEngine_ProvisionDeprovision_Integration(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	// docker=nil → EnsureRunning is a no-op; we exercise the pool DDL path.
	e := dbprovision.NewPostgresEngine(pool, nil, dbprovision.Config{})

	const db, role, pw = "blog_int01", "blog_int01_u", "Pass1234"
	if err := e.Provision(ctx, db, role, pw); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	var dbCount, roleCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&dbCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_roles WHERE rolname = $1", role).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if dbCount != 1 || roleCount != 1 {
		t.Fatalf("after provision: db=%d role=%d, want 1/1", dbCount, roleCount)
	}

	if err := e.Deprovision(ctx, db, role); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&dbCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_roles WHERE rolname = $1", role).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if dbCount != 0 || roleCount != 0 {
		t.Fatalf("after deprovision: db=%d role=%d, want 0/0", dbCount, roleCount)
	}
}
