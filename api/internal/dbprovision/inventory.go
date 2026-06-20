package dbprovision

import (
	"context"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// sizeTTL is how long a computed size map is reused before a fresh probe. Disk
// sizes move slowly, so a short cache keeps the box-wide Database page cheap to
// open without staleness that matters.
const sizeTTL = 30 * time.Second

// Inventory is the box-wide database view (plan 20): one group per engine, each
// listing the databases it hosts. The control-plane vac-db is pinned on the
// Postgres group.
type Inventory struct {
	Engines []EngineGroup
}

// EngineGroup is one engine tab's worth of data.
type EngineGroup struct {
	Engine      string
	FootprintMB int
	Shared      bool
	Databases   []DBEntry
}

// DBEntry is one database row. SizeBytes is nil when the engine couldn't size it
// (never reported as zero). LastBackup is nil when the DB has no backup config or
// has never run. IsControlPlane marks the synthetic vac-db entry.
type DBEntry struct {
	ID             string
	AppID          string
	AppSlug        string
	AppName        string
	DBName         string
	EnvVarName     string
	Status         string
	SizeBytes      *int64
	LastBackup     *BackupSummary
	IsControlPlane bool
}

// BackupSummary is the latest backup run for a database's engine container.
type BackupSummary struct {
	Status     string
	FinishedAt *time.Time
	SizeBytes  *int64
}

// DatabaseInventory returns every managed DB grouped by engine, joined to its app,
// with a latest-backup summary and computed disk size. The list and status are
// fresh per call; only the size map is cached (sizeTTL), so a provisioning→ready
// transition surfaces on the next poll while size probes stay cheap.
func (p *Provisioner) DatabaseInventory(ctx context.Context) (Inventory, error) {
	all, err := p.store.ListAllManagedDatabases(ctx)
	if err != nil {
		return Inventory{}, err
	}

	byEngine := make(map[string][]store.ManagedDatabaseWithApp, len(p.order))
	for _, m := range all {
		byEngine[m.Engine] = append(byEngine[m.Engine], m)
	}
	sizes := p.cachedSizes(ctx, all)

	var inv Inventory
	seen := make(map[string]bool, len(p.order))
	for _, name := range p.order {
		eng, ok := p.engines[name]
		if !ok {
			continue
		}
		seen[name] = true
		g := EngineGroup{Engine: name, FootprintMB: eng.FootprintMB(), Shared: eng.Shared()}
		// Pin the control-plane Postgres ahead of any user database so the operator
		// can never confuse VAC's own store with an app's.
		if name == "postgres" {
			g.Databases = append(g.Databases, DBEntry{
				DBName:         p.ctrlDB,
				Status:         "ready",
				IsControlPlane: true,
				SizeBytes:      sizeOf(sizes[name], p.ctrlDB),
			})
		}
		for _, m := range byEngine[name] {
			g.Databases = append(g.Databases, DBEntry{
				ID:         m.ID,
				AppID:      m.AppID,
				AppSlug:    m.AppSlug,
				AppName:    m.AppName,
				DBName:     m.DBName,
				EnvVarName: m.EnvVarName,
				Status:     m.Status,
				SizeBytes:  sizeOf(sizes[name], m.DBName),
				LastBackup: p.lastBackup(ctx, m.AppID, m.DBName, eng.BackupContainer()),
			})
		}
		inv.Engines = append(inv.Engines, g)
	}
	// Surface databases whose engine is no longer registered rather than hiding
	// them — honest inventory beats a tidy list.
	for engine, dbs := range byEngine {
		if seen[engine] {
			continue
		}
		g := EngineGroup{Engine: engine}
		for _, m := range dbs {
			g.Databases = append(g.Databases, DBEntry{
				ID: m.ID, AppID: m.AppID, AppSlug: m.AppSlug, AppName: m.AppName,
				DBName: m.DBName, EnvVarName: m.EnvVarName, Status: m.Status,
			})
		}
		inv.Engines = append(inv.Engines, g)
	}
	return inv, nil
}

// cachedSizes returns the per-engine size map, recomputing it (under the mutex, so
// concurrent callers single-flight) when the cache is older than sizeTTL.
func (p *Provisioner) cachedSizes(ctx context.Context, all []store.ManagedDatabaseWithApp) map[string]map[string]int64 {
	p.sizeMu.Lock()
	defer p.sizeMu.Unlock()
	if p.sizeCache != nil && time.Since(p.sizeAt) < sizeTTL {
		return p.sizeCache
	}

	names := map[string][]string{"postgres": {p.ctrlDB}}
	for _, m := range all {
		names[m.Engine] = append(names[m.Engine], m.DBName)
	}

	out := make(map[string]map[string]int64, len(names))
	for engine, dbNames := range names {
		eng, ok := p.engines[engine]
		if !ok {
			continue
		}
		sz, err := eng.SizeBytes(ctx, dbNames)
		if err != nil {
			// A down/unreachable engine just means "size unknown" — keep the rest.
			p.logger.Warn("dbprovision: size probe failed", "engine", engine, "err", err)
			continue
		}
		out[engine] = sz
	}
	p.sizeCache = out
	p.sizeAt = time.Now()
	return out
}

// lastBackup resolves the latest backup run for a managed DB via its backup
// config, which is keyed on the DB name (post-00080) with a fallback to the engine
// container name for legacy rows. Returns nil for "no config" or "never run".
func (p *Provisioner) lastBackup(ctx context.Context, appID, dbName, container string) *BackupSummary {
	cfg, err := p.store.GetBackupConfigForService(ctx, appID, dbName)
	if err != nil {
		// Legacy container-keyed config (pre-00080).
		cfg, err = p.store.GetBackupConfigForService(ctx, appID, container)
		if err != nil {
			return nil
		}
	}
	run, err := p.store.LatestBackupRun(ctx, cfg.ID)
	if err != nil {
		return nil
	}
	return &BackupSummary{Status: run.Status, FinishedAt: run.FinishedAt, SizeBytes: run.SizeBytes}
}

// sizeOf looks up one database's size, returning nil (unknown) when absent.
func sizeOf(m map[string]int64, name string) *int64 {
	if m == nil {
		return nil
	}
	v, ok := m[name]
	if !ok {
		return nil
	}
	return &v
}
