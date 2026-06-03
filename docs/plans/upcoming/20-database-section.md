# 20 — Database section (multi-engine overview in the sidebar)

**Tier:** Managed Services / observability · **Effort:** M · **Status:** planned (detailed)

## Goal

Promote the current single `/database` page (which only describes VAC's own control-plane
Postgres) into a real **Database section** with one tab per **live** engine — today
**Postgres** and **MariaDB** — each showing the databases that engine hosts, their **disk
usage**, their **backups**, and **links back to the owning app**. Clearly **highlight the
Postgres instance VAC itself runs on** (`vac-db`) so the operator never confuses the
control-plane store with a user database.

Tabs are **data-driven off the registered engine catalog**, so if a future engine is added
(e.g. a Redis add-on), its tab appears automatically with no UI change. **Redis is explicitly
out of scope here** — it's unregistered today; revisit only if/when it ships as an add-on.

Today managed databases are only visible **per-app** (`app-detail/databases-tab.tsx`); there
is no box-wide "what's living in my databases" view. This adds that operator-level lens.

## Why it matters (strategy)

Trust + density. A solo operator on one VPS needs to answer "what's eating my disk, what's
backed up, and which app owns this database" without SSH-ing in and running `psql`. It turns
the managed-DB feature (plan 09, shipped) from a per-app convenience into an at-a-glance
inventory — and the VAC-vs-user-Postgres highlight prevents the scariest mistake (touching
the control-plane DB thinking it's an app's).

## Current state (what we build on)

Verified against source:

- **Per-app store model.** `store.ManagedDatabase` (`api/internal/store/managed_dbs.go:14`)
  holds `ID / AppID / Engine / DBName / RoleName / EnvVarName / Status / Error / CreatedAt`.
  The only read paths today are app-scoped (`ListManagedDatabasesForApp`,
  `managed_dbs.go:78`) or an aggregate count (`CountManagedDatabasesByEngine`, `:100`). **There
  is no box-wide list joined to the owning app** — that's the first new store method.
- **Backups.** `store.BackupConfig` + `store.BackupRun` (`api/internal/store/backups.go`).
  `BackupRun.SizeBytes *int64` (`:50`) is the artifact size; `LatestBackupRun` (`:250`) and
  `ListBackupRuns` (`:225`) already exist. A managed DB's seed backup is keyed on
  `(app, engine.BackupContainer())` — **one backup config per engine container per app**, not
  per database (`dbprovision/provisioner.go:211`). So multiple same-engine DBs on one app
  share a single backup config; the section must not imply each DB is independently dumped.
- **Engine abstraction.** `dbprovision.Engine` (`api/internal/dbprovision/engine.go:30`) is a
  recipe interface. `Provisioner` registers `postgres` + `mariadb` only
  (`provisioner.go:73`); `order` (`:81`) is the stable catalog order. Mongo/Redis are
  framework-ready but **unregistered**. Postgres runs DDL through a `PGExecutor` pool into
  `vac-db` (`postgres.go:13`); MariaDB runs through `docker exec` via `DockerController`
  (`shared.go:15`, `execOK` at `:36`, which discards stdout). **No engine exposes a size probe
  today** — that's the one genuinely new engine capability.
- **HTTP surface.** Managed-DB routes are app-scoped and **gated by `cfg.ManagedServices`**
  (`server/server.go:307`). Handlers live in `server/handler/databases.go`; the provisioner is
  injected as the `DBProvisioner` interface (`databases.go:20`) and may be nil (the route block
  nil-checks `dbHandler`). There is **no global `/api/databases`** endpoint yet.
- **Control-plane Postgres** is `vac-db`, reached via `cfg.DatabaseURL`. User Postgres DBs are
  created **inside that same instance** by default (`PostgresEngine`, `postgres.go:24`); an
  isolated instance is a documented opt-in. `vac-db` has **no `AppID`** — exactly the hook that
  lets us pin it as the "VAC system database" card.
- **UI today.** Standalone `ui/src/features/database/database-page.tsx` (host disk + static
  copy only) mounted at `ui/src/routes/_app/database.tsx`; per-app list in
  `app-detail/databases-tab.tsx`; API client in `ui/src/lib/api/databases.ts` (app-scoped).
  Sidebar nav always shows **Database** (`components/layout/sidebar.tsx:23`); Add-ons is the
  managed-services-gated entry (`:28,:43`). Reusable Radix `Tabs` at
  `ui/src/components/ui/tabs.tsx`; brand glyphs via `BrandIcon`; `StatusPill`, `Card`,
  `EmptyState`, and `formatBytes` (`lib/format`) all exist and are used by the per-app tab.

## The gap to close — per-database disk usage

**No per-database disk usage is computed today** — only static `FootprintMB` warnings at
provision time. This is the one new backend capability. Add it as a method on the existing
`Engine` interface so it stays a per-engine *recipe*, not control-plane branching:

```go
// SizeBytes returns on-disk size per database name (added to the Engine interface).
// An engine that can't size a database returns it absent from the map; the caller
// reports that as unknown (nil), never as zero.
SizeBytes(ctx context.Context, dbNames []string) (map[string]int64, error)
```

Both live engines can size per-database, so this is a plain interface method rather than an
optional capability:

- **Postgres** — one query through the existing pool:
  `SELECT datname, pg_database_size(datname) FROM pg_database WHERE datname = ANY($1)`.
  Cheap, no per-DB round trips. The same query, unfiltered, also yields the `vac`
  control-plane DB size for the VAC card.
- **MariaDB** — one `docker exec` of
  `SELECT table_schema, SUM(data_length+index_length) FROM information_schema.tables GROUP BY table_schema`,
  parsed from tab-separated stdout. Reuse the exec path but capture stdout instead of
  `io.Discard` — add an `execOut` helper next to `execOK` in `shared.go`. Approximate (InnoDB
  rounds to extents) — label as "approx."

**Probing is not a hot path; cache only the probe, keep the list live.** The decision (see
*Decisions* below) is **lazy-on-open**, and to split the two concerns so the size cache never
staleness the status pill:

- The DB list + status + last-backup summary come straight from the **cheap store join, fresh
  every request** — so a `provisioning → ready` transition settles on the next poll.
- Only the expensive **disk sizes** come from a **30s cache**, single-flighted so a burst of
  page opens triggers one probe, then merged into the response. Sizes change slowly, so a
  just-ready DB honestly showing "—" for a few seconds is fine while status stays live.
- No background goroutine — zero footprint when the page is never opened (matches the backup
  scheduler's "start only if needed" ethos, `backup/scheduler.go`).

## Rough shape — backend

1. **Store: box-wide inventory query.** Add to `managed_dbs.go`:
   ```go
   type ManagedDatabaseWithApp struct {
       store.ManagedDatabase
       AppSlug string
       AppName string
   }
   func (s *Store) ListAllManagedDatabases(ctx context.Context) ([]ManagedDatabaseWithApp, error)
   ```
   `SELECT … FROM managed_databases m JOIN apps a ON a.id = m.app_id ORDER BY m.engine, a.slug`.
   Mirror the existing scan helpers; add a unit test next to the others.

2. **Engine: `SizeBytes` + provisioner inventory.** Implement `SizeBytes` on `PostgresEngine`
   and `MariaDBEngine`; add `execOut` (capture stdout) to `shared.go`. The `Provisioner` grows:
   ```go
   // DatabaseInventory returns, per engine, every managed DB joined to its app, a latest-backup
   // summary, and computed size (nil when unknown). Includes a synthetic control-plane entry
   // for vac-db. The list/status is fresh per call; only the size map is 30s-cached.
   func (p *Provisioner) DatabaseInventory(ctx context.Context) (Inventory, error)
   ```
   `Inventory` is grouped by engine: `[]EngineGroup{ Engine, Info (FootprintMB/Shared),
   Databases []DBEntry }`. `DBEntry` = `{ ID, AppID, AppSlug, DBName, EnvVarName, Status,
   SizeBytes *int64, LastBackup *BackupSummary, IsControlPlane bool }`. The provisioner is the
   right home — it already owns the engine map and the store slice (`provisioner.go:55`).

3. **Control-plane Postgres entry.** Inject a synthetic `DBEntry` on the Postgres group with
   `IsControlPlane: true`, no `AppID`, `DBName: "vac"` (the control DB from `cfg.DatabaseURL`),
   sized via the same `pg_database_size` query. Mark it so the UI pins it.

4. **Handler + route.** New `GET /api/databases` in `databases.go`:
   ```go
   func DatabaseInventory(prov DBProvisioner) http.HandlerFunc
   ```
   Extend the `DBProvisioner` interface (`databases.go:20`) with `DatabaseInventory(ctx)`.
   Register **globally** (not under `/apps/{id}`) and **behind the same gate**:
   `if cfg.ManagedServices && dbHandler != nil { r.Get("/databases", …) }` — alongside the
   existing app block (`server.go:307`) or in the global section near `/addons` (`:342`). DTO
   mirrors `Inventory`; sizes are `*int64` → JSON `null` when unknown, **never `0`**.

## Rough shape — frontend

- **API client.** New `ui/src/lib/api/db-inventory.ts` (leave per-app `databases.ts`
  untouched): `useDatabaseInventory()` → `api.get<DatabaseInventory>('databases')`, with a
  `refetchInterval` only while any entry is `provisioning` (mirror `databases.ts:24`). Add the
  DTO types to `ui/src/types/api.ts`.

- **Route content.** Rewrite `ui/src/features/database/database-page.tsx` to an engine-tabbed
  view using the existing Radix `Tabs`. Tabs are **data-driven off the inventory's engine
  groups**, not hardcoded — a tab appears only for engines the backend returns. Each tab:
  - a **table of databases**: name, owning app as a `<Link to="/apps/$appId">`, **size**
    (`formatBytes`, "—" with a tooltip "size not available for this engine" when null), status
    pill (reuse `pillStatus` from `databases-tab.tsx:38`),
  - a **backups panel** reusing the run history already rendered in `app-detail/backups-tab.tsx`
    (extract the run-list into a shared `components/common/backup-runs.tsx` if inlined today),
  - **total disk used by that engine** in the tab header (sum of known sizes; show "+ N
    unknown" when some are null).

- **VAC highlight.** On the Postgres tab, render the `IsControlPlane` entry as a **pinned,
  visually distinct card** above the user-DB table — badge "VAC system database — used by the
  control plane", muted, no remove affordance, its own size + backup summary, no app link.
  Today's page shows a **"Backups: Nightly / volume snapshot" stat tile
  (`database-page.tsx:30`) that is static placeholder copy — nothing actually backs up
  `vac-db`** (confirmed: seed backups are per managed-DB, none target the control DB). **Delete
  that misleading tile**; the VAC card shows the honest state — "no backup configured" with a
  CTA to set one up via the existing backup machinery (which can already target `vac-db`,
  `backup/engine.go:127`).

- **Sidebar.** Keep **Database** always visible (it already is, `sidebar.tsx:23`). With managed
  services off, the page degrades to today's control-plane-only view (below), so no sidebar
  gating change is needed.

- **Flag-off / empty states.**
  - `VAC_MANAGED_SERVICES` **off** → `GET /api/databases` isn't mounted. Detect via the
    existing `useInstanceInfo().managed_services` flag (`lib/api/instance.ts`) and fall back to
    the **current** single-Postgres view (host disk + VAC card) — never blank.
  - Managed services **on**, zero user DBs → the Postgres tab still shows the pinned VAC card;
    a live engine with zero DBs shows an empty table ("No databases on this engine yet").

## Phasing (ship in slices)

1. **Backend inventory** — store `ListAllManagedDatabases`, `SizeBytes` on PG+Maria, the
   size-cached `DatabaseInventory`, `GET /api/databases` behind the gate. Unit-test the store
   query and the size-cache TTL/single-flight; extend the existing `postgres_integration_test.go`
   for the PG size probe.
2. **Frontend tabs** — inventory client, engine-tabbed page, app links, per-engine totals, VAC
   card, flag-off fallback.
3. **Backups panel** — extract/reuse the run history; wire per-engine and VAC-card backups.

Redis (and any other engine) needs **no work here** — the moment it's registered in
`dbprovision`, its tab appears automatically.

## Resolved (was open; settled by reading the code)

- **Control-plane backup state** — **nothing backs up `vac-db` today.** Seed backups are
  per managed-DB and target their own container (`provisioner.go:211`); none target the
  control DB. The current page's "Nightly / volume snapshot" tile (`database-page.tsx:30`) is
  **static placeholder copy and misleading** — delete it. The VAC card shows the honest "no
  backup configured" state + CTA; the backup machinery can already target `vac-db`
  (`backup/engine.go:127`) if the operator opts in.
- **`info` Badge variant** — confirmed **absent** in `components/ui/badge.tsx` (only
  `success`/green exists, no blue). It's a definite add, shared with plan 21 — not optional.

## Decisions (the former open questions, now settled)

- **Probe cadence** — **lazy-on-open**, and cache only the size probe (30s, single-flighted)
  while serving the DB list/status fresh per request. No background polling — it wastes CPU/disk
  on a one-operator box when nobody's looking, and a whole-inventory cache would lag the
  provisioning status pill. See *The gap to close*.
- **Redis** — **out of scope.** Unregistered today; not faked as a tab. Tabs are data-driven,
  so a future Redis add-on engine surfaces automatically with no further UI work.
- **Granularity** — **stop at per-database.** Disk usage + owning app + backups per database is
  the operator-level lens this page is for; anyone needing per-schema/per-table detail connects
  to the database directly with their own tool. Not VAC's job to reimplement a SQL client.

## Acceptance (sketch)

Operator opens **Database** in the sidebar and sees a tab per live engine (Postgres, MariaDB).
The Postgres tab lists each user database with its size and owning-app link, and pins a
clearly-labelled card for VAC's own control-plane Postgres. Each tab shows total disk for that
engine and a backups panel. Sizes the engine can't report read "—" with an honest tooltip,
never a misleading `0`. Turning `VAC_MANAGED_SERVICES` off degrades gracefully to the
control-plane-only view (host disk + VAC card), and the page is never blank.
</content>
