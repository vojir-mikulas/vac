package dbprovision

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RedisEngine is a shared, lazily-started cache engine: one vac-redis daemon,
// multi-tenant by ACL (the same shape as MariaDB, but Redis instead of SQL).
//
// Isolation is by key prefix: each app gets its own ACL user, locked to keys and
// channels under a private "<dbName>:" prefix and denied the @dangerous commands
// (FLUSHALL/FLUSHDB/CONFIG/…) that would reach across tenants. The prefix the app
// must namespace under is injected as a second env var (ExtraEnv) since a redis://
// URL can't carry it. The admin password is derived from the master key (stable
// across restarts); per-app users are ACL SAVE'd to an aclfile in the data volume,
// and data persists via AOF — so both creds and data survive a restart.
//
// Redis opts OUT of VAC's nightly logical backups (SkipsBackup): its data can't
// round-trip through the plain-dump→stdin-restore pipeline the SQL engines use, so
// VAC treats it as a managed cache, not a system of record, rather than seeding a
// backup it couldn't honestly restore.
type RedisEngine struct {
	docker  DockerController
	workDir string
	edge    string
	adminPw string
}

const (
	redisContainer = "vac-redis"
	redisProject   = "vac-managed-redis"
	redisImage     = "redis:7-alpine"
)

// NewRedisEngine wires the Redis recipe.
func NewRedisEngine(docker DockerController, cfg Config) *RedisEngine {
	return &RedisEngine{
		docker:  docker,
		workDir: cfg.WorkDir,
		edge:    cfg.EdgeNetwork,
		adminPw: deriveAdminPassword(cfg.MasterKey, "redis"),
	}
}

func (e *RedisEngine) Name() string { return "redis" }

func (e *RedisEngine) composeYAML() string {
	// vac-edge is external (owned by the VAC stack); the daemon joins it with a
	// stable alias so apps reach it the same way they reach each other.
	//
	// On first boot we seed an aclfile in the data volume with the default (admin)
	// user — password derived from the master key — then start redis-server against
	// it. Per-app users created later are ACL SAVE'd to that file. requirepass is
	// deliberately omitted: Redis rejects it alongside an aclfile-managed default
	// user. The admin password is hex (deriveAdminPassword), so it's safe to embed
	// in the double-quoted echo string without escaping.
	return fmt.Sprintf(`name: %s
services:
  redis:
    image: %s
    container_name: %s
    restart: unless-stopped
    command:
      - sh
      - -c
      - |
        test -f /data/users.acl || echo "user default on >%s allkeys allchannels allcommands" > /data/users.acl
        exec redis-server --appendonly yes --aclfile /data/users.acl
    volumes:
      - data:/data
    networks:
      %s:
        aliases:
          - %s
volumes:
  data:
networks:
  %s:
    external: true
`, redisProject, redisImage, redisContainer, e.adminPw, e.edge, redisContainer, e.edge)
}

// cli builds a redis-cli invocation authenticated as the admin (default) user.
// --no-auth-warning suppresses the stderr notice that -a prints; the admin
// password is hex, so it needs no quoting.
func (e *RedisEngine) cli(args string) string {
	return fmt.Sprintf("redis-cli --no-auth-warning -a %s %s", e.adminPw, args)
}

// EnsureRunning lazily brings up the shared daemon (first use only) and waits for
// it to accept authenticated commands.
func (e *RedisEngine) EnsureRunning(ctx context.Context) error {
	dir, err := writeComposeProject(e.workDir, "redis", e.composeYAML())
	if err != nil {
		return err
	}
	if err := e.docker.Up(ctx, dir, "compose.yaml", redisProject, ""); err != nil {
		return fmt.Errorf("dbprovision: redis up: %w", err)
	}
	if err := pingUntilReady(ctx, e.docker, redisContainer, e.cli("PING"), 60*time.Second); err != nil {
		return fmt.Errorf("dbprovision: redis not ready: %w", err)
	}
	return nil
}

// Provision creates the app's ACL user, confined to its "<dbName>:" key/channel
// prefix and denied @dangerous commands, then persists it to the aclfile. The
// same password is baked into ConnString, so the app can authenticate as this
// user. '>'/'~'/'&'/'*' are single-quoted so the container shell (sh -c) treats
// them literally rather than as redirection / glob / background operators; dbName
// and roleName are asserted safe and password is drawn from a quote-free alphabet.
func (e *RedisEngine) Provision(ctx context.Context, dbName, roleName, password string) error {
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	setuser := e.cli(fmt.Sprintf(
		"ACL SETUSER %s reset on '>%s' '~%s:*' '&%s:*' +@all -@dangerous",
		roleName, password, dbName, dbName,
	))
	cmd := setuser + " && " + e.cli("ACL SAVE")
	if err := execOK(ctx, e.docker, redisContainer, cmd); err != nil {
		return fmt.Errorf("dbprovision: redis provision: %w", err)
	}
	return nil
}

// Deprovision drops the app's keys (scoped to its prefix, so no other tenant is
// touched) and deletes its ACL user. Best-effort: the key sweep is tolerant of an
// empty keyspace, and only DELUSER/SAVE gate the result.
func (e *RedisEngine) Deprovision(ctx context.Context, dbName, roleName string) error {
	if err := mustBeIdent(dbName, roleName); err != nil {
		return err
	}
	// '~'/'*' single-quoted so the container shell treats the pattern literally;
	// the trailing redirect discards the sweep's noise. The ';' runs DELUSER even
	// when the keyspace was empty, and '&&' persists the deletion.
	sweep := e.cli("--scan --pattern '"+dbName+":*'") + " | xargs " + e.cli("UNLINK") + " >/dev/null 2>&1"
	cmd := sweep + "; " + e.cli("ACL DELUSER "+roleName) + " && " + e.cli("ACL SAVE")
	if err := execOK(ctx, e.docker, redisContainer, cmd); err != nil {
		return fmt.Errorf("dbprovision: redis deprovision: %w", err)
	}
	return nil
}

// SizeBytes reports nothing: Redis interleaves every tenant's keys in one
// keyspace, so there's no cheap per-tenant on-disk size. Returning an empty map
// (never an error) reports each database as unknown rather than guessed, keeping
// the box-wide inventory cheap and honest.
func (e *RedisEngine) SizeBytes(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// ConnString is the URL the app dials, authenticating as its own ACL user. The
// key prefix it's confined to rides alongside as a separate env var (ExtraEnv),
// since the redis:// scheme can't express it.
func (e *RedisEngine) ConnString(_, roleName, password string) string {
	return fmt.Sprintf("redis://%s:%s@%s:6379", roleName, password, redisContainer)
}

// ExtraEnv injects the key prefix the app must namespace under — the other half
// of key-prefix isolation. The var name is derived from the connection-string
// binding (REDIS_URL → REDIS_PREFIX, else <binding>_PREFIX) so it's unique per
// managed cache and removable on deprovision.
func (e *RedisEngine) ExtraEnv(binding string, names GeneratedNames) map[string]string {
	return map[string]string{redisPrefixVar(binding): names.DBName + ":"}
}

// SkipsBackup opts Redis out of the nightly logical backup the provisioner seeds
// for SQL engines (see the type doc).
func (e *RedisEngine) SkipsBackup() bool { return true }

// The backup methods exist only to satisfy Engine. MatchBackupCommand never
// matches, so restore/verify are never offered for Redis ("only restore what we
// can invert"); the rest are unreachable for Redis because SkipsBackup stops a
// backup config ever being seeded.
func (e *RedisEngine) DefaultBackupCommand(string) string       { return "" }
func (e *RedisEngine) MatchBackupCommand(string) (string, bool) { return "", false }
func (e *RedisEngine) RestoreCommand(string) string             { return "" }
func (e *RedisEngine) VerifyRestoreCommand(string) string       { return "" }
func (e *RedisEngine) BackupContainer() string                  { return redisContainer }

func (e *RedisEngine) EnvVarName() string { return "REDIS_URL" }
func (e *RedisEngine) FootprintMB() int   { return 30 }
func (e *RedisEngine) Shared() bool       { return true }

// redisPrefixVar derives the key-prefix env var name from the connection-string
// binding: REDIS_URL → REDIS_PREFIX, ANALYTICS_URL → ANALYTICS_PREFIX, and any
// other binding → <binding>_PREFIX.
func redisPrefixVar(binding string) string {
	if base, ok := strings.CutSuffix(binding, "_URL"); ok {
		return base + "_PREFIX"
	}
	return binding + "_PREFIX"
}
