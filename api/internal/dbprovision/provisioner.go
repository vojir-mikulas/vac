package dbprovision

import (
	"context"
	"errors"
	"log/slog"
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
)

// Store is the persistence slice the provisioner needs. *store.Store satisfies it.
type Store interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	CreateManagedDatabase(ctx context.Context, appID, engine, dbName string, roleName *string, secretEnc []byte, envVarName string) (store.ManagedDatabase, error)
	SetManagedDatabaseStatus(ctx context.Context, id, status string, errMsg *string) error
	GetManagedDatabase(ctx context.Context, id string) (store.ManagedDatabase, error)
	ListManagedDatabasesForApp(ctx context.Context, appID string) ([]store.ManagedDatabase, error)
	DeleteManagedDatabase(ctx context.Context, appID, id string) error
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
}

// New builds a provisioner with the default engine set (Postgres + MariaDB).
// Mongo/Redis are intentionally not registered in v1 — the recipe framework
// supports adding them as data when demand appears (see docs/deviations.md).
func New(s Store, box *crypto.Box, pool PGExecutor, docker interface {
	NetAttacher
	DockerController
}, cfg Config, logger *slog.Logger) *Provisioner {
	if logger == nil {
		logger = slog.Default()
	}
	engines := map[string]Engine{
		"postgres": NewPostgresEngine(pool, docker, cfg),
		"mariadb":  NewMariaDBEngine(docker, cfg),
	}
	return &Provisioner{
		store:   s,
		box:     box,
		engines: engines,
		order:   []string{"postgres", "mariadb"},
		logger:  logger,
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

// Add creates a managed DB row in the `provisioning` state and runs the actual
// provisioning in the background (cold-starting a shared daemon can take tens of
// seconds). The caller polls the row's status. Returns ErrUnsupportedEngine /
// ErrEncryptionDisabled / store.ErrConflict synchronously.
func (p *Provisioner) Add(ctx context.Context, app store.App, engineName string) (store.ManagedDatabase, error) {
	eng, ok := p.engines[engineName]
	if !ok {
		return store.ManagedDatabase{}, ErrUnsupportedEngine
	}
	if p.box == nil {
		return store.ManagedDatabase{}, ErrEncryptionDisabled
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
	row, err := p.store.CreateManagedDatabase(ctx, app.ID, engineName, names.DBName, &role, sealed, eng.EnvVarName())
	if err != nil {
		return store.ManagedDatabase{}, err
	}
	go p.provision(row, app, eng, names, sealed)
	return row, nil
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
	// Seed a daily local backup so the DB is covered with no manual config. The
	// config is keyed on (app, BackupContainer); a second managed DB sharing the
	// same container collides — fine, the first one's backup already exists.
	_, err := p.store.CreateBackupConfig(ctx, app.ID, store.BackupConfigInput{
		ServiceName: eng.BackupContainer(),
		Command:     eng.DefaultBackupCommand(names.DBName),
		Frequency:   "daily",
		HourOfDay:   3,
		Destination: "local",
		KeepCount:   7,
		Enabled:     true,
	})
	if err != nil && !errors.Is(err, store.ErrConflict) {
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
		_ = p.store.DeleteBackupConfigForService(ctx, appID, eng.BackupContainer())
	}
	_ = p.store.DeleteEnvVar(ctx, appID, m.EnvVarName)
	return p.store.DeleteManagedDatabase(ctx, appID, id)
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
