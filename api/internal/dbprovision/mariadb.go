package dbprovision

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// safeIdentRe is the safe-identifier shape generateNames produces: a lowercase
// letter followed by [a-z0-9_]. mariadb interpolates db/role names into a shell
// `mariadb -e "..."` sink with no quoting (unlike Postgres' pgx.Identifier.
// Sanitize), so we assert the shape at the boundary. Today the names are always
// generated and safe; this keeps a future "custom DB name" feature from turning
// into container command injection.
var safeIdentRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func mustBeIdent(names ...string) error {
	for _, n := range names {
		if !safeIdentRe.MatchString(n) {
			return fmt.Errorf("dbprovision: refusing unsafe identifier %q", n)
		}
	}
	return nil
}

// MariaDBEngine is the worked example of a shared, lazily-started engine: one
// vac-mariadb daemon, multi-tenant by database, provisioned by `docker exec`ing
// the mariadb admin CLI (decision #6). The admin password is derived from the
// master key so it's stable across restarts without separate storage.
type MariaDBEngine struct {
	docker  DockerController
	workDir string
	edge    string
	adminPw string
}

const (
	mariadbContainer = "vac-mariadb"
	mariadbProject   = "vac-managed-mariadb"
	mariadbImage     = "mariadb:11"
)

// NewMariaDBEngine wires the MariaDB recipe.
func NewMariaDBEngine(docker DockerController, cfg Config) *MariaDBEngine {
	return &MariaDBEngine{
		docker:  docker,
		workDir: cfg.WorkDir,
		edge:    cfg.EdgeNetwork,
		adminPw: deriveAdminPassword(cfg.MasterKey, "mariadb"),
	}
}

func (e *MariaDBEngine) Name() string { return "mariadb" }

func (e *MariaDBEngine) composeYAML() string {
	// vac-edge is external (owned by the VAC stack); the daemon joins it with a
	// stable alias so apps reach it the same way they reach each other.
	return fmt.Sprintf(`name: %s
services:
  mariadb:
    image: %s
    container_name: %s
    restart: unless-stopped
    environment:
      MARIADB_ROOT_PASSWORD: "%s"
    volumes:
      - data:/var/lib/mysql
    networks:
      %s:
        aliases:
          - %s
volumes:
  data:
networks:
  %s:
    external: true
`, mariadbProject, mariadbImage, mariadbContainer, e.adminPw, e.edge, mariadbContainer, e.edge)
}

// EnsureRunning lazily brings up the shared daemon (first use only) and writes a
// root client config inside the container so dumps/admin don't carry the
// password on the command line.
func (e *MariaDBEngine) EnsureRunning(ctx context.Context) error {
	dir, err := writeComposeProject(e.workDir, "mariadb", e.composeYAML())
	if err != nil {
		return err
	}
	if err := e.docker.Up(ctx, dir, "compose.yaml", mariadbProject, ""); err != nil {
		return fmt.Errorf("dbprovision: mariadb up: %w", err)
	}
	ping := fmt.Sprintf("mariadb -uroot -p%s -e 'SELECT 1'", e.adminPw)
	if err := pingUntilReady(ctx, e.docker, mariadbContainer, ping, 90*time.Second); err != nil {
		return fmt.Errorf("dbprovision: mariadb not ready: %w", err)
	}
	// Root client config so `mariadb-dump {db}` needs no password on the CLI.
	cnf := fmt.Sprintf(`printf '[client]\nuser=root\npassword=%s\n' > /root/.my.cnf`, e.adminPw)
	if err := execOK(ctx, e.docker, mariadbContainer, cnf); err != nil {
		return fmt.Errorf("dbprovision: mariadb client config: %w", err)
	}
	return nil
}

func (e *MariaDBEngine) Provision(ctx context.Context, dbName, roleName, password string) error {
	// Names are sanitized to [a-z0-9_] so they need no backtick quoting (which
	// would otherwise trigger command substitution inside the container shell).
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	sql := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS %s; CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%'; FLUSH PRIVILEGES;",
		dbName, roleName, password, dbName, roleName,
	)
	cmd := fmt.Sprintf(`mariadb -uroot -p%s -e "%s"`, e.adminPw, sql)
	if err := execOK(ctx, e.docker, mariadbContainer, cmd); err != nil {
		return fmt.Errorf("dbprovision: mariadb provision: %w", err)
	}
	return nil
}

func (e *MariaDBEngine) Deprovision(ctx context.Context, dbName, roleName string) error {
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s; DROP USER IF EXISTS '%s'@'%%'; FLUSH PRIVILEGES;", dbName, roleName)
	cmd := fmt.Sprintf(`mariadb -uroot -p%s -e "%s"`, e.adminPw, sql)
	if err := execOK(ctx, e.docker, mariadbContainer, cmd); err != nil {
		return fmt.Errorf("dbprovision: mariadb deprovision: %w", err)
	}
	return nil
}

// SizeBytes sums data_length+index_length per schema from information_schema in a
// single query. InnoDB rounds allocation to extents, so the figure is approximate
// — the UI labels it as such. Schemas with no tables don't appear in
// information_schema.tables, so a freshly-created empty DB is reported as 0 here
// only if it has at least one table; otherwise it's omitted (unknown).
func (e *MariaDBEngine) SizeBytes(ctx context.Context, dbNames []string) (map[string]int64, error) {
	if len(dbNames) == 0 {
		return map[string]int64{}, nil
	}
	// -N (skip column names) -B (batch/tab-separated) gives clean "schema\tbytes" rows.
	query := "SELECT table_schema, COALESCE(SUM(data_length+index_length),0) FROM information_schema.tables GROUP BY table_schema"
	cmd := fmt.Sprintf(`mariadb -uroot -p%s -N -B -e "%s"`, e.adminPw, query)
	stdout, err := execOut(ctx, e.docker, mariadbContainer, cmd)
	if err != nil {
		return nil, fmt.Errorf("dbprovision: mariadb size probe: %w", err)
	}
	want := make(map[string]bool, len(dbNames))
	for _, n := range dbNames {
		want[n] = true
	}
	out := make(map[string]int64, len(dbNames))
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !want[fields[0]] {
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

func (e *MariaDBEngine) ConnString(dbName, roleName, password string) string {
	return fmt.Sprintf("mysql://%s:%s@%s:3306/%s", roleName, password, mariadbContainer, dbName)
}

func (e *MariaDBEngine) DefaultBackupCommand(dbName string) string {
	// Reads root creds from /root/.my.cnf written at EnsureRunning, so no
	// password lands in the stored backup command.
	return fmt.Sprintf("mariadb-dump %s", dbName)
}

// MatchBackupCommand recognizes the `mariadb-dump <db>` shape this engine emits
// and extracts the database name; any other command returns ok=false so restore
// is refused (decision #1).
func (e *MariaDBEngine) MatchBackupCommand(cmd string) (string, bool) {
	db, ok := strings.CutPrefix(strings.TrimSpace(cmd), "mariadb-dump ")
	if !ok || !safeIdentRe.MatchString(db) {
		return "", false
	}
	return db, true
}

// RestoreCommand replays a mariadb-dump from stdin. mariadb-dump emits
// `DROP TABLE IF EXISTS` before each table by default, so a plain replay through
// the client overwrites existing data. Reads root creds from /root/.my.cnf
// (written at EnsureRunning), like the dump — no password on the CLI.
func (e *MariaDBEngine) RestoreCommand(dbName string) string {
	return fmt.Sprintf("mariadb %s", dbName)
}

// VerifyRestoreCommand creates a fresh scratch database, replays the dump into
// it, then always drops it — preserving the replay's exit status so an
// unrestorable dump is reported as failed. mariadb-dump of a single DB carries
// no CREATE DATABASE/USE, so the replay lands in the scratch DB we connect to.
func (e *MariaDBEngine) VerifyRestoreCommand(scratchDB string) string {
	create := fmt.Sprintf("mariadb -e 'CREATE DATABASE %s'", scratchDB)
	apply := fmt.Sprintf("mariadb %s", scratchDB)
	drop := fmt.Sprintf("mariadb -e 'DROP DATABASE IF EXISTS %s'", scratchDB)
	return fmt.Sprintf("%s && { %s; rc=$?; %s >/dev/null 2>&1; exit $rc; }", create, apply, drop)
}

func (e *MariaDBEngine) BackupContainer() string { return mariadbContainer }
func (e *MariaDBEngine) EnvVarName() string      { return "DATABASE_URL" }
func (e *MariaDBEngine) FootprintMB() int        { return 150 }
func (e *MariaDBEngine) Shared() bool            { return true }
