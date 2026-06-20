package dbprovision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

var (
	// ErrUnsupportedEngine is returned for an engine name with no registered recipe.
	ErrUnsupportedEngine = errors.New("dbprovision: unsupported engine")
	// ErrEncryptionDisabled is returned when VAC_MASTER_KEY is unset — managed DBs
	// can't be created because the connection secret can't be sealed.
	ErrEncryptionDisabled = errors.New("dbprovision: encryption disabled (VAC_MASTER_KEY unset)")
	// ErrInvalidBindingName is returned for a requested env-var binding that isn't
	// a legal shell/identifier env var name.
	ErrInvalidBindingName = errors.New("dbprovision: invalid binding name (must match [A-Z_][A-Z0-9_]*)")
)

// bindingNameRe is the legal shape for the env var a connection string is
// injected as — a conventional uppercase env var identifier.
var bindingNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Store is the persistence slice the provisioner needs. *store.Store satisfies it.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	CreateManagedDatabase(ctx context.Context, appID, engine, dbName string, roleName *string, secretEnc []byte, envVarName string) (store.ManagedDatabase, error)
	SetManagedDatabaseStatus(ctx context.Context, id, status string, errMsg *string) error
	GetManagedDatabase(ctx context.Context, id string) (store.ManagedDatabase, error)
	ListManagedDatabasesForApp(ctx context.Context, appID string) ([]store.ManagedDatabase, error)
	ListAllManagedDatabases(ctx context.Context) ([]store.ManagedDatabaseWithApp, error)
	DeleteManagedDatabase(ctx context.Context, appID, id string) error
	GetBackupConfigForService(ctx context.Context, appID, service string) (store.BackupConfig, error)
	LatestBackupRun(ctx context.Context, configID string) (store.BackupRun, error)
	UpsertEnvVar(ctx context.Context, appID, key string, value []byte, sensitive bool) error
	DeleteEnvVar(ctx context.Context, appID, key string) error
	CreateBackupConfig(ctx context.Context, appID string, in store.BackupConfigInput) (store.BackupConfig, error)
	DeleteBackupConfigForService(ctx context.Context, appID, service string) error
}

// EngineInfo is the catalog/footprint view of a registered engine.
type EngineInfo struct {
	Name        string `json:"name"`
	FootprintMB int    `json:"footprint_mb"`
	Shared      bool   `json:"shared"`
}

// Provisioner ties engines to persistence: it generates identities, seals the
// connection string, drives the engine, injects the env var, and seeds a D1
// backup — so a managed DB is "covered by backups with no manual config".
type Provisioner struct {
	store   Store
	box     *crypto.Box
	engines map[string]Engine
	order   []string // stable engine order for the catalog
	logger  *slog.Logger
	ctrlDB  string // control-plane Postgres DB name (vac), pinned in the inventory

	// Size probe cache (plan 20): disk sizes are expensive to compute, so the
	// box-wide inventory serves the list/status fresh but caches the size map for
	// sizeTTL. The mutex doubles as a single-flight guard during recompute.
	sizeMu    sync.Mutex
	sizeAt    time.Time
	sizeCache map[string]map[string]int64 // engine -> dbName -> bytes
}

// New builds a provisioner with the default engine set (Postgres + MariaDB +
// Redis). Mongo is intentionally not registered — the recipe framework supports
// adding it as data when demand appears (see docs/deviations.md).
func New(s Store, box *crypto.Box, pool PGExecutor, docker interface {
	NetAttacher
	DockerController
}, cfg Config, logger *slog.Logger,
) *Provisioner {
	if logger == nil {
		logger = slog.Default()
	}
	// The Postgres recipe has two shapes: the shared control-plane vac-db (default,
	// zero extra footprint) or an isolated vac-db-managed daemon for blast-radius
	// isolation. Exactly one is registered under "postgres" per the opt-in.
	var postgres Engine = NewPostgresEngine(pool, docker, cfg)
	if cfg.ManagedDBIsolated {
		postgres = NewIsolatedPostgresEngine(docker, cfg)
	}
	engines := map[string]Engine{
		"postgres": postgres,
		"mariadb":  NewMariaDBEngine(docker, cfg),
		"redis":    NewRedisEngine(docker, cfg),
	}
	ctrlDB := cfg.PostgresControlDB
	if ctrlDB == "" {
		ctrlDB = "vac"
	}
	return &Provisioner{
		store:   s,
		box:     box,
		engines: engines,
		order:   []string{"postgres", "mariadb"},
		logger:  logger,
		ctrlDB:  ctrlDB,
	}
}

// AvailableEngines lists the registered engines for the UI picker, in a stable
// order (Postgres first — the free default).
func (p *Provisioner) AvailableEngines() []EngineInfo {
	out := make([]EngineInfo, 0, len(p.order))
	for _, name := range p.order {
		if e, ok := p.engines[name]; ok {
			out = append(out, EngineInfo{Name: e.Name(), FootprintMB: e.FootprintMB(), Shared: e.Shared()})
		}
	}
	return out
}

// EngineInfoFor returns the catalog entry for one engine.
func (p *Provisioner) EngineInfoFor(name string) (EngineInfo, bool) {
	e, ok := p.engines[name]
	if !ok {
		return EngineInfo{}, false
	}
	return EngineInfo{Name: e.Name(), FootprintMB: e.FootprintMB(), Shared: e.Shared()}, true
}

// RestoreCommandFor maps a stored backup command to the command that replays its
// artifact, or ("", false) when no registered engine recognizes it as one of its
// defaults (backup-restore decision #1 — VAC only restores what it knows how to
// invert). Satisfies the backup package's RestoreCommandResolver.
func (p *Provisioner) RestoreCommandFor(backupCommand string) (string, bool) {
	for _, name := range p.order {
		eng, ok := p.engines[name]
		if !ok {
			continue
		}
		if db, ok := eng.MatchBackupCommand(backupCommand); ok {
			return eng.RestoreCommand(db), true
		}
	}
	return "", false
}

// VerifyCommandFor maps a stored backup command to a non-destructive
// restorability check that replays the dump into scratchDB, or ("", false) when
// no registered engine recognizes the command. Satisfies the backup package's
// VerifyCommandResolver.
func (p *Provisioner) VerifyCommandFor(backupCommand, scratchDB string) (string, bool) {
	for _, name := range p.order {
		eng, ok := p.engines[name]
		if !ok {
			continue
		}
		if _, ok := eng.MatchBackupCommand(backupCommand); ok {
			return eng.VerifyRestoreCommand(scratchDB), true
		}
	}
	return "", false
}

// Add creates a managed DB row in the `provisioning` state and runs the actual
// provisioning in the background (cold-starting a shared daemon can take tens of
// seconds). The caller polls the row's status.
//
// envVarName is the env var the connection string is injected as; pass "" to let
// the provisioner pick a unique default (DATABASE_URL, then a suffixed name if
// taken). Returns ErrUnsupportedEngine / ErrEncryptionDisabled /
// ErrInvalidBindingName / store.ErrConflict (binding already used) synchronously.
func (p *Provisioner) Add(ctx context.Context, app store.App, engineName, envVarName string) (store.ManagedDatabase, error) {
	eng, ok := p.engines[engineName]
	if !ok {
		return store.ManagedDatabase{}, ErrUnsupportedEngine
	}
	if p.box == nil {
		return store.ManagedDatabase{}, ErrEncryptionDisabled
	}
	binding, err := p.resolveBindingName(ctx, app.ID, eng, envVarName)
	if err != nil {
		return store.ManagedDatabase{}, err
	}
	names, err := generateNames(app.Slug)
	if err != nil {
		return store.ManagedDatabase{}, err
	}
	// The connection string is deterministic from the generated identity + the
	// engine's fixed alias, so it can be sealed before provisioning runs.
	conn := eng.ConnString(names.DBName, names.RoleName, names.Password)
	sealed, err := p.box.Seal([]byte(conn))
	if err != nil {
		return store.ManagedDatabase{}, err
	}
	role := names.RoleName
	row, err := p.store.CreateManagedDatabase(ctx, app.ID, engineName, names.DBName, &role, sealed, binding)
	if err != nil {
		return store.ManagedDatabase{}, err
	}
	go p.provision(row, app, eng, names, sealed)
	return row, nil
}

// resolveBindingName picks the env var the connection string will be injected as,
// guaranteeing uniqueness within the app so a second managed DB can't silently
// overwrite the first (the historic DATABASE_URL collision).
//
//   - requested != "": validated; rejected with store.ErrConflict if already
//     taken by another DB on the app, ErrInvalidBindingName if malformed.
//   - requested == "": defaults to the engine's name (DATABASE_URL); if that is
//     taken, falls back to DATABASE_URL_<ENGINE>, then DATABASE_URL_2, _3, …
func (p *Provisioner) resolveBindingName(ctx context.Context, appID string, eng Engine, requested string) (string, error) {
	existing, err := p.store.ListManagedDatabasesForApp(ctx, appID)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(existing))
	for _, m := range existing {
		used[m.EnvVarName] = true
	}
	if requested != "" {
		if !bindingNameRe.MatchString(requested) {
			return "", ErrInvalidBindingName
		}
		if used[requested] {
			return "", store.ErrConflict
		}
		return requested, nil
	}
	base := eng.EnvVarName()
	if !used[base] {
		return base, nil
	}
	if suffixed := base + "_" + strings.ToUpper(eng.Name()); !used[suffixed] {
		return suffixed, nil
	}
	for i := 2; ; i++ {
		if cand := fmt.Sprintf("%s_%d", base, i); !used[cand] {
			return cand, nil
		}
	}
}

// provision performs the engine-side work and flips the row to ready/error.
func (p *Provisioner) provision(row store.ManagedDatabase, app store.App, eng Engine, names GeneratedNames, sealed []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := eng.EnsureRunning(ctx); err != nil {
		p.fail(ctx, row.ID, err)
		return
	}
	if err := eng.Provision(ctx, names.DBName, names.RoleName, names.Password); err != nil {
		p.fail(ctx, row.ID, err)
		return
	}
	// Inject the connection string so the next redeploy picks it up — sensitive,
	// so the API won't echo it back on list.
	if err := p.store.UpsertEnvVar(ctx, app.ID, row.EnvVarName, sealed, true); err != nil {
		p.fail(ctx, row.ID, err)
		return
	}
	// Inject any engine-specific extra env (Redis's key prefix). Sealed at rest
	// like everything else, but not marked sensitive — the prefix is config the
	// operator should be able to see, not a secret.
	if extra, ok := eng.(extraEnver); ok {
		for k, v := range extra.ExtraEnv(row.EnvVarName, names) {
			es, serr := p.box.Seal([]byte(v))
			if serr != nil {
				p.fail(ctx, row.ID, fmt.Errorf("seal env %s: %w", k, serr))
				return
			}
			if err := p.store.UpsertEnvVar(ctx, app.ID, k, es, false); err != nil {
				p.fail(ctx, row.ID, err)
				return
			}
		}
	}
	// Engines whose data can't round-trip through the dump/restore pipeline (Redis)
	// opt out of the seeded backup rather than have VAC claim a backup it couldn't
	// restore. Flip to ready and stop here for them.
	if nb, ok := eng.(unbackuppable); ok && nb.SkipsBackup() {
		if err := p.store.SetManagedDatabaseStatus(ctx, row.ID, "ready", nil); err != nil {
			p.logger.Warn("dbprovision: mark ready", "id", row.ID, "err", err)
		}
		p.logger.Info("dbprovision: provisioned (no backup)", "app", app.Slug, "engine", eng.Name(), "db", names.DBName)
		return
	}
	// Seed a daily local backup so the DB is covered with no manual config. The
	// config is keyed on (app, DBName) — unique per managed DB — with the shared
	// engine container as the explicit exec target. This is what lets two managed
	// DBs of the same engine on one app each get their own backup (they share the
	// container but no longer the config key).
	container := eng.BackupContainer()
	_, err := p.store.CreateBackupConfig(ctx, app.ID, store.BackupConfigInput{
		ServiceName:   names.DBName,
		ContainerName: &container,
		Command:       eng.DefaultBackupCommand(names.DBName),
		Frequency:     "daily",
		HourOfDay:     3,
		Destination:   "local",
		KeepCount:     7,
		Enabled:       true,
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		// Re-provisioning the same DB name (e.g. a retried provision): the backup is
		// already configured, nothing to do.
		p.logger.Info("dbprovision: backup already configured for this database",
			"app", app.Slug, "engine", eng.Name(), "db", names.DBName)
	case err != nil:
		p.logger.Warn("dbprovision: seed backup config", "app", app.Slug, "err", err)
	}
	if err := p.store.SetManagedDatabaseStatus(ctx, row.ID, "ready", nil); err != nil {
		p.logger.Warn("dbprovision: mark ready", "id", row.ID, "err", err)
	}
	p.logger.Info("dbprovision: provisioned", "app", app.Slug, "engine", eng.Name(), "db", names.DBName)
}

func (p *Provisioner) fail(ctx context.Context, id string, cause error) {
	msg := cause.Error()
	if err := p.store.SetManagedDatabaseStatus(ctx, id, "error", &msg); err != nil {
		p.logger.Warn("dbprovision: mark error", "id", id, "err", err)
	}
	p.logger.Warn("dbprovision: provisioning failed", "id", id, "err", msg)
}

// Remove deprovisions a managed DB (engine-side drop + env var + backup config +
// row). Best-effort on the engine side so a half-broken instance can't pin the
// row.
func (p *Provisioner) Remove(ctx context.Context, appID, id string) error {
	m, err := p.store.GetManagedDatabase(ctx, id)
	if err != nil {
		return err
	}
	if m.AppID != appID {
		return store.ErrNotFound
	}
	if eng, ok := p.engines[m.Engine]; ok {
		role := ""
		if m.RoleName != nil {
			role = *m.RoleName
		}
		if err := eng.Deprovision(ctx, m.DBName, role); err != nil {
			p.logger.Warn("dbprovision: engine deprovision", "id", id, "err", err)
		}
		// The seeded backup is keyed on the DB name (see provision), so removing one
		// managed DB leaves any sibling DB's backup on the same engine container
		// untouched.
		_ = p.store.DeleteBackupConfigForService(ctx, appID, m.DBName)
		// Clear a legacy container-keyed config (pre-00080) only when this is the
		// last managed DB of its engine for the app — otherwise a sibling that still
		// relies on that shared config would lose it.
		if p.lastOfEngine(ctx, appID, m.Engine, m.ID) {
			_ = p.store.DeleteBackupConfigForService(ctx, appID, eng.BackupContainer())
		}
		// Remove any engine-specific extra env vars injected at provision (Redis's
		// key prefix). The values don't matter here — only the keys, which are
		// derived from the binding.
		if extra, ok := eng.(extraEnver); ok {
			for k := range extra.ExtraEnv(m.EnvVarName, GeneratedNames{DBName: m.DBName}) {
				_ = p.store.DeleteEnvVar(ctx, appID, k)
			}
		}
	}
	_ = p.store.DeleteEnvVar(ctx, appID, m.EnvVarName)
	return p.store.DeleteManagedDatabase(ctx, appID, id)
}

// lastOfEngine reports whether excludeID is the only managed DB of the given
// engine left for the app — used to decide if a shared legacy backup config is
// safe to remove. On a list error it returns false (keep the config; deleting on
// uncertainty is the riskier choice).
func (p *Provisioner) lastOfEngine(ctx context.Context, appID, engine, excludeID string) bool {
	dbs, err := p.store.ListManagedDatabasesForApp(ctx, appID)
	if err != nil {
		return false
	}
	for _, d := range dbs {
		if d.ID != excludeID && d.Engine == engine {
			return false
		}
	}
	return true
}

// DeprovisionApp drops the engine-side objects for every managed DB an app owns.
// Called before app delete — the VAC rows (managed_databases, env_vars, backup
// configs) cascade on the app delete, but the database/role inside the engine
// would otherwise be orphaned.
func (p *Provisioner) DeprovisionApp(ctx context.Context, appID string) {
	dbs, err := p.store.ListManagedDatabasesForApp(ctx, appID)
	if err != nil {
		p.logger.Warn("dbprovision: list for deprovision", "app", appID, "err", err)
		return
	}
	for _, m := range dbs {
		eng, ok := p.engines[m.Engine]
		if !ok {
			continue
		}
		role := ""
		if m.RoleName != nil {
			role = *m.RoleName
		}
		if err := eng.Deprovision(ctx, m.DBName, role); err != nil {
			p.logger.Warn("dbprovision: deprovision on app delete", "id", m.ID, "err", err)
		}
	}
}
