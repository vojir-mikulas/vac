package dbprovision

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PGExecutor is the slice of *pgxpool.Pool the Postgres engine needs to run DDL
// and the size probe. *pgxpool.Pool satisfies it.
type PGExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// NetAttacher attaches a container to a docker network with an alias.
// *dockercli.Compose satisfies it.
type NetAttacher interface {
	NetworkConnect(ctx context.Context, network, container, alias string) error
}

// PostgresEngine provisions a new database + role inside the shared control-plane
// vac-db using VAC's own pool — no new process (decision #6). vac-db is attached
// to vac-edge so the app can reach it by alias.
type PostgresEngine struct {
	pool      PGExecutor
	docker    NetAttacher
	edge      string
	host      string // vac-edge alias apps dial (vac-db)
	adminUser string // superuser inside vac-db (vac)
}

// NewPostgresEngine wires the Postgres recipe.
func NewPostgresEngine(pool PGExecutor, docker NetAttacher, cfg Config) *PostgresEngine {
	host := cfg.PostgresHost
	if host == "" {
		host = "vac-db"
	}
	admin := cfg.PostgresAdminUser
	if admin == "" {
		admin = "vac"
	}
	return &PostgresEngine{pool: pool, docker: docker, edge: cfg.EdgeNetwork, host: host, adminUser: admin}
}

func (e *PostgresEngine) Name() string { return "postgres" }

// EnsureRunning makes sure vac-db is reachable from user apps by attaching it to
// vac-edge with a stable alias. The control DB sharing vac-edge is the conscious
// "shared with control plane" default (isolated instance is a documented opt-in).
func (e *PostgresEngine) EnsureRunning(ctx context.Context) error {
	if e.docker == nil || e.edge == "" {
		return nil // tests / no-docker: nothing to attach
	}
	return e.docker.NetworkConnect(ctx, e.edge, e.host, e.host)
}

func (e *PostgresEngine) Provision(ctx context.Context, dbName, roleName, password string) error {
	// CREATE ROLE then CREATE DATABASE — neither can run inside a transaction, so
	// each is a separate simple Exec. Identifiers are sanitized; the password is
	// quote-free by construction but still single-quoted defensively.
	role := pgx.Identifier{roleName}.Sanitize()
	db := pgx.Identifier{dbName}.Sanitize()
	if _, err := e.pool.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD %s", role, quoteLiteral(password))); err != nil {
		return fmt.Errorf("dbprovision: create role: %w", err)
	}
	if _, err := e.pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", db, role)); err != nil {
		// Roll back the orphan role so a retry with fresh names isn't blocked.
		_, _ = e.pool.Exec(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", role))
		return fmt.Errorf("dbprovision: create database: %w", err)
	}
	return nil
}

func (e *PostgresEngine) Deprovision(ctx context.Context, dbName, roleName string) error {
	db := pgx.Identifier{dbName}.Sanitize()
	role := pgx.Identifier{roleName}.Sanitize()
	// FORCE (PG 13+) drops live connections so a busy app can't pin the database.
	if _, err := e.pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db)); err != nil {
		return fmt.Errorf("dbprovision: drop database: %w", err)
	}
	if _, err := e.pool.Exec(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", role)); err != nil {
		return fmt.Errorf("dbprovision: drop role: %w", err)
	}
	return nil
}

// SizeBytes reports on-disk size per database via pg_database_size — one query,
// no per-DB round trips. Databases absent from pg_database (dropped, or never
// created) are simply not in the returned map.
func (e *PostgresEngine) SizeBytes(ctx context.Context, dbNames []string) (map[string]int64, error) {
	out := make(map[string]int64, len(dbNames))
	if len(dbNames) == 0 {
		return out, nil
	}
	rows, err := e.pool.Query(ctx,
		`SELECT datname, pg_database_size(datname) FROM pg_database WHERE datname = ANY($1)`, dbNames)
	if err != nil {
		return nil, fmt.Errorf("dbprovision: pg size probe: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			return nil, err
		}
		out[name] = size
	}
	return out, rows.Err()
}

func (e *PostgresEngine) ConnString(dbName, roleName, password string) string {
	// Names/password are drawn from URL-safe alphabets, so no escaping needed.
	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", roleName, password, e.host, dbName)
}

func (e *PostgresEngine) DefaultBackupCommand(dbName string) string {
	// Runs inside vac-db over the local socket as the superuser (trust auth in the
	// official image), so no password lands in the backup command.
	return fmt.Sprintf("pg_dump -U %s %s", e.adminUser, dbName)
}

// MatchBackupCommand recognizes the `pg_dump -U <admin> <db>` shape this engine
// emits and extracts the database name. A custom command (different flags, env
// vars, -Fc, …) returns ok=false so restore is refused (decision #1). The db
// name must be a safe generated identifier — it's interpolated into a shell
// command in RestoreCommand.
func (e *PostgresEngine) MatchBackupCommand(cmd string) (string, bool) {
	prefix := fmt.Sprintf("pg_dump -U %s ", e.adminUser)
	db, ok := strings.CutPrefix(strings.TrimSpace(cmd), prefix)
	if !ok || !safeIdentRe.MatchString(db) {
		return "", false
	}
	return db, true
}

// RestoreCommand replays a plain pg_dump from stdin. pg_dump's default output
// carries no DROP statements, so restoring into a populated database would
// collide on existing objects; reset the public schema first (as the superuser
// the dump ran as), then replay. ON_ERROR_STOP makes a mid-stream failure a
// non-zero exit so the restore run is recorded as failed, not silently partial.
func (e *PostgresEngine) RestoreCommand(dbName string) string {
	reset := fmt.Sprintf(
		"psql -U %s -d %s -v ON_ERROR_STOP=1 -c 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;'",
		e.adminUser, dbName)
	apply := fmt.Sprintf("psql -U %s -d %s -v ON_ERROR_STOP=1", e.adminUser, dbName)
	return reset + " && " + apply
}

func (e *PostgresEngine) BackupContainer() string { return e.host }
func (e *PostgresEngine) EnvVarName() string      { return "DATABASE_URL" }
func (e *PostgresEngine) FootprintMB() int        { return 0 }
func (e *PostgresEngine) Shared() bool            { return false }

// quoteLiteral single-quotes a SQL string literal, doubling any embedded quotes.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
