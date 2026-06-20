package dbprovision

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// IsolatedPostgresEngine is the blast-radius-isolated alternative to
// PostgresEngine: instead of provisioning into the shared control-plane vac-db
// (where user data would sit alongside VAC's own users/sessions/audit rows), it
// runs a separate vac-db-managed daemon dedicated to managed databases — the
// VAC_MANAGED_DB_ISOLATED opt-in (docs/deviations.md).
//
// It follows the lazy-daemon recipe shape of MariaDBEngine (compose up on first
// use, provision by `docker exec`ing psql) rather than PostgresEngine's
// control-plane pool, since the control plane has no pool to that instance. The
// dump/restore command shapes are shared with PostgresEngine (only the superuser
// differs — "postgres", the official image default).
type IsolatedPostgresEngine struct {
	docker  DockerController
	workDir string
	edge    string
	adminPw string
}

const (
	isolatedPGContainer = "vac-db-managed"
	isolatedPGProject   = "vac-managed-postgres"
	isolatedPGImage     = "postgres:16-alpine"
	isolatedPGAdmin     = "postgres" // official image superuser
)

// NewIsolatedPostgresEngine wires the isolated Postgres recipe.
func NewIsolatedPostgresEngine(docker DockerController, cfg Config) *IsolatedPostgresEngine {
	return &IsolatedPostgresEngine{
		docker:  docker,
		workDir: cfg.WorkDir,
		edge:    cfg.EdgeNetwork,
		adminPw: deriveAdminPassword(cfg.MasterKey, "postgres-isolated"),
	}
}

func (e *IsolatedPostgresEngine) Name() string { return "postgres" }

func (e *IsolatedPostgresEngine) composeYAML() string {
	// vac-edge is external (owned by the VAC stack); the daemon joins it with a
	// stable alias so apps reach it the same way they reach the shared engines.
	return fmt.Sprintf(`name: %s
services:
  postgres:
    image: %s
    container_name: %s
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: "%s"
    volumes:
      - data:/var/lib/postgresql/data
    networks:
      %s:
        aliases:
          - %s
volumes:
  data:
networks:
  %s:
    external: true
`, isolatedPGProject, isolatedPGImage, isolatedPGContainer, e.adminPw, e.edge, isolatedPGContainer, e.edge)
}

// EnsureRunning lazily brings up the dedicated daemon (first use only) and waits
// for it to accept connections. Idempotent — `compose up` on an already-running
// project is a no-op.
func (e *IsolatedPostgresEngine) EnsureRunning(ctx context.Context) error {
	dir, err := writeComposeProject(e.workDir, "postgres", e.composeYAML())
	if err != nil {
		return err
	}
	if err := e.docker.Up(ctx, dir, "compose.yaml", isolatedPGProject, ""); err != nil {
		return fmt.Errorf("dbprovision: isolated postgres up: %w", err)
	}
	// Local socket connections use trust auth in the official image, so the admin
	// CLI needs no password.
	ping := fmt.Sprintf("psql -U %s -c 'SELECT 1'", isolatedPGAdmin)
	if err := pingUntilReady(ctx, e.docker, isolatedPGContainer, ping, 90*time.Second); err != nil {
		return fmt.Errorf("dbprovision: isolated postgres not ready: %w", err)
	}
	return nil
}

func (e *IsolatedPostgresEngine) Provision(ctx context.Context, dbName, roleName, password string) error {
	// Names are sanitized to [a-z0-9_] so they need no quoting; the password is
	// drawn from a quote-free alphabet but single-quoted defensively as a literal.
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	// CREATE ROLE then CREATE DATABASE — neither can run inside a transaction, so
	// each is its own psql -c invocation (a fresh connection/implicit transaction).
	createRole := fmt.Sprintf(`psql -U %s -c "CREATE ROLE %s LOGIN PASSWORD '%s'"`, isolatedPGAdmin, roleName, password)
	if err := execOK(ctx, e.docker, isolatedPGContainer, createRole); err != nil {
		return fmt.Errorf("dbprovision: isolated postgres create role: %w", err)
	}
	createDB := fmt.Sprintf(`psql -U %s -c "CREATE DATABASE %s OWNER %s"`, isolatedPGAdmin, dbName, roleName)
	if err := execOK(ctx, e.docker, isolatedPGContainer, createDB); err != nil {
		// Roll back the orphan role so a retry with fresh names isn't blocked.
		_ = execOK(ctx, e.docker, isolatedPGContainer, fmt.Sprintf(`psql -U %s -c "DROP ROLE IF EXISTS %s"`, isolatedPGAdmin, roleName))
		return fmt.Errorf("dbprovision: isolated postgres create database: %w", err)
	}
	return nil
}

func (e *IsolatedPostgresEngine) Deprovision(ctx context.Context, dbName, roleName string) error {
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	// FORCE (PG 13+) drops live connections so a busy app can't pin the database.
	dropDB := fmt.Sprintf(`psql -U %s -c "DROP DATABASE IF EXISTS %s WITH (FORCE)"`, isolatedPGAdmin, dbName)
	if err := execOK(ctx, e.docker, isolatedPGContainer, dropDB); err != nil {
		return fmt.Errorf("dbprovision: isolated postgres drop database: %w", err)
	}
	dropRole := fmt.Sprintf(`psql -U %s -c "DROP ROLE IF EXISTS %s"`, isolatedPGAdmin, roleName)
	if err := execOK(ctx, e.docker, isolatedPGContainer, dropRole); err != nil {
		return fmt.Errorf("dbprovision: isolated postgres drop role: %w", err)
	}
	return nil
}

// SizeBytes reports on-disk size per database via pg_database_size in a single
// query. Databases absent from pg_database are omitted (reported as unknown).
func (e *IsolatedPostgresEngine) SizeBytes(ctx context.Context, dbNames []string) (map[string]int64, error) {
	out := make(map[string]int64, len(dbNames))
	if len(dbNames) == 0 {
		return out, nil
	}
	lits := make([]string, 0, len(dbNames))
	for _, n := range dbNames {
		if !safeIdentRe.MatchString(n) {
			continue // never interpolate an unexpected name into the query
		}
		lits = append(lits, "'"+n+"'")
	}
	if len(lits) == 0 {
		return out, nil
	}
	// -tA = tuples-only, unaligned; -F'|' gives clean "name|bytes" rows.
	query := fmt.Sprintf("SELECT datname, pg_database_size(datname) FROM pg_database WHERE datname IN (%s)", strings.Join(lits, ","))
	cmd := fmt.Sprintf(`psql -U %s -tA -F'|' -c "%s"`, isolatedPGAdmin, query)
	stdout, err := execOut(ctx, e.docker, isolatedPGContainer, cmd)
	if err != nil {
		return nil, fmt.Errorf("dbprovision: isolated postgres size probe: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "|", 2)
		if len(fields) != 2 {
			continue
		}
		size, perr := strconv.ParseInt(fields[1], 10, 64)
		if perr != nil {
			continue
		}
		out[fields[0]] = size
	}
	return out, nil
}

func (e *IsolatedPostgresEngine) ConnString(dbName, roleName, password string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", roleName, password, isolatedPGContainer, dbName)
}

func (e *IsolatedPostgresEngine) DefaultBackupCommand(dbName string) string {
	return pgDefaultBackupCommand(isolatedPGAdmin, dbName)
}

func (e *IsolatedPostgresEngine) MatchBackupCommand(cmd string) (string, bool) {
	return pgMatchBackupCommand(isolatedPGAdmin, cmd)
}

func (e *IsolatedPostgresEngine) RestoreCommand(dbName string) string {
	return pgRestoreCommand(isolatedPGAdmin, dbName)
}

func (e *IsolatedPostgresEngine) VerifyRestoreCommand(scratchDB string) string {
	return pgVerifyRestoreCommand(isolatedPGAdmin, scratchDB)
}

func (e *IsolatedPostgresEngine) BackupContainer() string { return isolatedPGContainer }
func (e *IsolatedPostgresEngine) EnvVarName() string      { return "DATABASE_URL" }

// FootprintMB / Shared report a real added daemon (unlike the shared Postgres,
// which rides vac-db at no extra cost) so the UI warns before confirm.
func (e *IsolatedPostgresEngine) FootprintMB() int { return 30 }
func (e *IsolatedPostgresEngine) Shared() bool     { return true }
