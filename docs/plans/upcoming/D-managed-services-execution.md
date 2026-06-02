# Track D — Managed Services — execution plan

> Working plan for executing **Track D** of [`00-parallel-tracks.md`](00-parallel-tracks.md).
> Track D is **greenfield**: it adds new packages (`backup`, `dbprovision`, `addon`) and their
> UI, and barely touches existing code — so it parallelizes cleanly with the other tracks. It is
> **internally sequenced by dependency**: D2 backs up via D1's dump primitive; D3's "dashboards
> from managed DBs" needs D2.
>
> Sequence: **D1 `08` Managed backups → D2 `09` Managed databases → D3 `12` Add-on catalog.**
> Owns: new `api/internal/backup`, `api/internal/dbprovision`, `api/internal/addon` packages;
> a `docker exec` primitive in `dockercli`; new store tables; an embedded template registry;
> and new UI feature folders (`backups`, `databases`, `addons`).
>
> **Strategy gate (from the stubs):** D is the monetization arc. Build it in parallel, but treat
> shipping as gated on Tracks A/B being trustworthy and on **real user demand** — `09`'s stub is
> explicit ("do NOT initially build … build when users ask"). Concretely: land each item behind a
> config flag (`VAC_MANAGED_SERVICES=off` by default) and keep the nav entries hidden until the
> gate opens. The flag also keeps the `<200 MB` control-plane claim honest — none of D's
> background goroutines start when it's off.

## Pre-flight: locked decisions (resolve the stubs' open questions before coding)

Decided up front so nothing churns mid-track.

1. **Migration range — reserve `00040`–`00049` for Track D.** Latest used on `main` is `00034`
   (Track C's `onboarding_dismissed`); Tracks A/B consumed `00030`–`00033`. Starting Track D at
   `00040` leaves a deliberate gap so no track rebases another's numbers at merge. D uses three
   migrations (`00040` backups, `00041` managed DBs, `00042` addon installs); the rest is slack.

2. **Scheduler = reuse the retention-pruner pattern, no cron dependency.**
   `api/internal/retention/pruner.go` already establishes the house style: a `Run(ctx)` goroutine
   that computes `timeUntilNext(now, …)`, `select`s on `time.After(wait)` vs `ctx.Done()`, runs
   one pass, repeats — with an injectable `now func() time.Time` for tests. The backup scheduler
   copies this. **Schedule model = structured interval, not a cron string:** columns
   `(frequency ∈ daily|weekly, hour_of_day INT, day_of_week INT NULL)`. This avoids pulling a
   cron parser (the project hand-rolls to dodge deps — cf. Track B writing Prometheus exposition
   by hand) and matches the pruner's "next occurrence" math. Power-user cron expressions are a
   documented later extension, not v1.

3. **Backup destinations: `local` first (zero-dep), `s3` second (one focused dep).**
   - **`local`** — write the dump to a VAC-managed host directory / Docker volume
     (`{WorkDir}/backups/{slug}/{service}/{ts}.dump`). Zero dependencies; the honest default for a
     single box; also the staging path for S3.
   - **`s3`** — S3-compatible (AWS S3 / Backblaze B2 / MinIO all share the API). Use a **single
     focused client** (`minio-go`) rather than the full AWS SDK (smaller, one API surface, B2/MinIO
     work out of the box). Credentials + bucket/endpoint stored encrypted via `crypto.Box`, the
     same way env vars/webhook secrets already are.
   - Destination is an interface (`backup.Destination` with `Put(ctx, key, io.Reader) error`) so a
     third (rsync/SFTP) drops in later without touching the dump engine.

4. **Restore is out of scope for v1 — except "download the latest dump" + a documented manual
   command.** The acceptance bar is *produce* trustworthy artifacts and *surface* them; an
   automated restore-into-a-live-DB flow is a sharp footgun (overwrites live data) and waits for
   explicit demand. v1 ships a signed download link for any retained artifact and a copy-paste
   "how to restore" snippet derived from the engine.

5. **Backups are engine-agnostic: a user shell command run via `docker exec`, captured to stdout.**
   Straight from `mvp.md` § Backups (V2): the user supplies e.g. `pg_dump -U $POSTGRES_USER
   $POSTGRES_DB`; VAC runs it **inside the running service container** and ships stdout. **No
   volume-level tarring** (risks inconsistent state). This needs a `docker exec` primitive that
   does not exist yet (see D1) — `dockercli.Compose` today has Build/Up/Ps/Logs/Stats/Inspect but
   **no Exec**.

6. **Managed Postgres = shared `vac-db`, per-app DB+role (default); isolated instance = opt-in.**
   The cheapest path and a non-migration (`mvp.md` § Why shared Postgres). Other engines = **one
   shared daemon per engine, lazy-started, multi-tenant by database** — never a container per app.
   This is the load-bearing cost decision from the `09` stub; restated as a locked default here.

7. **Catalog templates are embedded data, deployed through the existing pipeline as user apps —
   never per-add-on Go code.** A template = `compose.yaml` + manifest + a provisioning bundle,
   `go:embed`ed into the binary and materialized into the work dir at install. Same guardrail as
   the build adapters and DB engines: recipes, not bespoke code in `vac-api`.

8. **Managed-services background goroutines are demand-gated, like stats.** The backup scheduler
   and any engine-health watcher only run when `VAC_MANAGED_SERVICES` is on **and** at least one
   backup/managed-DB exists. Zero footprint on a box that uses none — protects the RAM budget.

---

## D1 — `08` Managed backups  *(effort M)*

**Goal:** a user-defined backup command per service, run on a schedule, captured and shipped to a
destination, with success/failure surfaced and a notification on failure. This is the
engine-agnostic dump primitive D2 reuses.

### Design decisions

- **`docker exec` capture, not volume tar** (decision #5). The command runs in the *running*
  container so the engine flushes a consistent dump; stdout is streamed straight to the
  destination writer (no full buffer in RAM — important on a 2 GB box).
- **Credentials come from the env vars VAC already stores** — no duplicate config. The exec runs
  inside the container, so `$POSTGRES_USER` etc. are already present in its environment.
- **Scheduler reuses the pruner pattern** (decision #2); **destinations are pluggable** (#3);
  **restore is download-only** (#4).
- **Retention/rotation at the destination:** keep the last *N* artifacts per (app, service)
  (default 7), prune older after a successful upload — the same "keep last N" shape as the image
  pruner (`image_keep_count`).

### New primitive: `dockercli.Exec`

Add `Exec(ctx, containerID string, cmd []string, stdout io.Writer) error` to
`api/internal/dockercli/compose.go` (alongside `Logs`/`Stats`, reusing the existing
`command(ctx, wd, args...)` helper): runs `docker exec {containerID} sh -c {cmd}`, streams stdout
to the writer, returns a non-nil error on non-zero exit. The container ID comes from the
`services.container_id` column the deploy pipeline already populates.

### Schema (migration `00040_managed_backups.sql`)

```sql
CREATE TABLE backup_configs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id        UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name  TEXT NOT NULL,
    command       TEXT NOT NULL,                 -- e.g. pg_dump -U $POSTGRES_USER $POSTGRES_DB
    frequency     TEXT NOT NULL,                 -- daily | weekly
    hour_of_day   INT  NOT NULL DEFAULT 3,
    day_of_week   INT,                           -- 0-6, NULL for daily
    destination   TEXT NOT NULL,                 -- local | s3
    dest_config   BYTEA,                         -- crypto.Box-sealed JSON (bucket/endpoint/keys)
    keep_count    INT  NOT NULL DEFAULT 7,
    enabled       BOOL NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, service_name)
);

CREATE TABLE backup_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id     UUID NOT NULL REFERENCES backup_configs(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'running',  -- running | success | failed
    size_bytes    BIGINT,
    artifact_key  TEXT,                              -- destination path / object key
    error         TEXT
);
```

`dest_config` is sealed with `crypto.Box` exactly like `apps.webhook_secret_enc` — the store
never sees plaintext credentials.

### Backend (new package `api/internal/backup`)

1. **`backup.Destination` interface** + `LocalDestination` and `S3Destination` implementations
   (decision #3). `Put(ctx, key string, r io.Reader) (size int64, err error)` and
   `Prune(ctx, prefix string, keep int) error`.
2. **`backup.Engine.RunOnce(ctx, cfg)`** — resolve the service's `container_id`, `dockercli.Exec`
   the command, stream stdout → destination `Put`, record a `backup_runs` row (running → success/
   failed), prune old artifacts, fire a notification **on failure**.
3. **`backup.Scheduler.Run(ctx)`** — pruner-pattern goroutine: load enabled configs, compute the
   next due time per config, sleep to the soonest, `RunOnce`, repeat. Wired in `main.go` next to
   the retention pruner, **guarded by `VAC_MANAGED_SERVICES`** (decision #8). Manual "Back up now"
   calls `RunOnce` directly off the worker.
4. **Notify:** add `EventBackupFailed` to `notify/events.go` + `AllEvents`, and
   `Dispatcher.BackupFailed(appName, appID, service, errMsg string)` following the `CrashLoop`
   shape. (Success is surfaced in-UI only — failure is the event that matters.)
5. **Store methods** (`api/internal/store/backups.go`): CRUD on `backup_configs`,
   `CreateBackupRun`/`FinishBackupRun`, `ListBackupRuns(appID)`, `ListDueBackupConfigs`.

### HTTP + audit

Handlers in `api/internal/server/handler/backups.go`, mounted in the authenticated `/api` group:
- `GET/POST /api/apps/{id}/backups` (config CRUD), `DELETE …/backups/{cid}`
- `POST /api/apps/{id}/backups/{cid}/run` (manual run → 202)
- `GET /api/apps/{id}/backups/{cid}/runs` (history)
- `GET /api/apps/{id}/backups/runs/{rid}/download` (signed/streamed artifact — decision #4)

Enrich with `audit.SetTarget(ctx, "backup", cid)` + `audit.Describe(ctx, "configured backup for
{slug}/{service}")`. The Stage-0 audit middleware records actor/route/outcome for free.

### UI (new feature `ui/src/features/backups/`)

- New **Backups tab** on app-detail (route `_app/apps/$appId/backups.tsx`): per-service backup
  config form (command, schedule, destination), a run-history table (status pill reusing
  `StatusPill`, size, timestamp, download), and a "Back up now" button.
- `ui/src/lib/api/backups.ts` — `useBackups(appId)`, `useCreateBackup`, `useRunBackup`,
  `useBackupRuns` (TanStack Query, `queryKeys.apps.backups(appId)`); types in `types/api.ts`
  (`BackupConfig`, `BackupRun`).
- **Tie off the MVP warning:** `mvp.md` promises "a visible warning on any stateful service with
  no backup configured." Surface that badge on the service card now that a backup *can* be
  configured.

### Tests

- `dockercli.Exec` (integration, real container, asserts stdout capture + non-zero-exit error).
- `backup.Engine.RunOnce` with a fake `Destination` + fake exec: success path records size/key;
  exec failure records `failed` + fires `BackupFailed`; prune keeps last N.
- `Scheduler` next-due math with injectable `now` (daily/weekly, hour/day-of-week).
- Handler: config CRUD validation; manual run 202; download streams the artifact.

### Acceptance

A configured backup command runs on schedule, lands an artifact in the chosen destination,
records success/failure, prunes to `keep_count`, and fires a notification on failure. The latest
artifact is downloadable from the UI.

### Files touched

`api/internal/db/migrations/00040_managed_backups.sql` (new), `api/internal/backup/*` (new),
`api/internal/dockercli/compose.go` (`Exec`), `api/internal/store/backups.go` (new),
`api/internal/notify/{events,dispatcher}.go` (`BackupFailed`),
`api/internal/server/handler/backups.go` (new) + route wiring in `server.go`,
`api/internal/config/config.go` (`ManagedServices` flag), `api/main.go` (scheduler goroutine),
plus UI: `features/backups/*`, `lib/api/backups.ts`, `types/api.ts`, service-card badge, new route.
**No `deploy`/`caddy`/`proxy` edits → no Track A collision.**

---

## D2 — `09` Managed databases  *(effort L — Postgres path M, +per engine)*

**Goal:** one-click "add a database to your app" — VAC provisions the engine the user picks,
injects the connection string as an env var, and backs it up via D1. The user *chooses the
engine*; VAC handles the rest.

### Design decisions (locked)

- **Postgres = no new process** (decision #6): a new database + role inside `vac-db`. The blessed,
  cheapest default.
- **Any other engine = one shared, lazily-started daemon per engine, multi-tenant by database** —
  `vac-mariadb`, `vac-redis`, `vac-mongo`. Cost = (distinct engines in use) processes, regardless
  of app count. Spun up the first time an app requests that engine; **footprint warned at add-time**
  ("starts a shared MariaDB instance, ~150 MB").
- **Provisioning runs by `docker exec` into the engine container** — consistent with D1 and forced
  by the architecture: `vac-api` is **off `vac-edge`**, so it can't open a socket to the engine
  directly. For the shared `vac-db` Postgres, VAC uses its **own pool** to run `CREATE DATABASE` /
  `CREATE ROLE` (the `vac` role needs `CREATEDB`/`CREATEROLE` — see open questions). For other
  engines, VAC `docker exec`s the admin CLI (`mysql -e …`, `mongosh --eval …`, `redis-cli`).
- **The app reaches its managed DB over `vac-edge` by DNS alias** — the same routing mechanism
  user services already use. The engine container joins `vac-edge` with a stable alias
  (`vac-mariadb`, `vac-redis`, …); the injected connection string points at that alias. (`vac-db`
  itself is reachable to user apps via an alias on `vac-edge` too — confirm/attach at provision
  time.)
- **Connection string is injected as an env var** via the existing `store.ReplaceEnvVars` /
  `env_vars` path (sealed with `crypto.Box`), so an app redeploy picks it up with **no manual
  config** — read-only role where the engine supports it.
- **Each engine is a small provisioning recipe** (data/config: image, admin-exec templates,
  connection-string template, footprint estimate), never bespoke code — same guardrail as build
  adapters and catalog templates.
- **Shared-with-control-plane is the default; isolated instance is opt-in** (`09` stub's conscious
  decision). A `VAC_MANAGED_DB_ISOLATED=true` knob points managed Postgres at a second
  `vac-db-managed` instance for blast-radius isolation — one extra process bought deliberately.

### Schema (migration `00041_managed_databases.sql`)

```sql
CREATE TABLE managed_databases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    engine          TEXT NOT NULL,                 -- postgres | mariadb | mongo | redis
    db_name         TEXT NOT NULL,                 -- provisioned database / keyspace
    role_name       TEXT,                          -- provisioned role (NULL for redis)
    secret_enc      BYTEA NOT NULL,                -- crypto.Box-sealed connection string + password
    env_var_name    TEXT NOT NULL DEFAULT 'DATABASE_URL',
    status          TEXT NOT NULL DEFAULT 'provisioning', -- provisioning | ready | error
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, engine, db_name)
);
```

The shared per-engine instances are **not** rows here — they're managed containers tracked by
fixed name (`vac-mariadb`, …); "is the engine up?" is a `docker inspect` query, not DB state.

### Backend (new package `api/internal/dbprovision`)

1. **`dbprovision.Engine` interface** — `Provision(ctx, dbName, roleName) (connString string,
   err error)`, `Deprovision(ctx, dbName, roleName) error`, `EnsureRunning(ctx) error`,
   `Footprint() int` (MB), `DefaultBackupCommand(dbName) string` (feeds D1).
   Implementations: `PostgresEngine` (pool DDL on `vac-db`), `MariaDBEngine`, `MongoEngine`,
   `RedisEngine` (each a recipe + `docker exec` templates).
2. **Lazy start** (`EnsureRunning`): for non-Postgres engines, if the shared container isn't
   running, generate a tiny compose project (`vac-managed-{engine}`) with a persistent volume and
   a `vac-edge` alias, `dockercli.Up` it, wait healthy. First-use only.
3. **Provision flow:** `EnsureRunning` → create DB + role + random password → seal connection
   string → write it as an `env_vars` row (`DATABASE_URL` by default) → mark `ready`. Auto-create
   a D1 `backup_config` seeded with `DefaultBackupCommand` (the "covered by backups with no manual
   config" acceptance line).
4. **Deprovision** on managed-DB delete *and* on app delete: drop the DB/role, remove the env var,
   delete the backup config. (App delete already cascades the row; add a deprovision hook so the
   engine-side objects are cleaned too, not just the VAC row.)
5. **Notify:** reuse `BackupFailed` from D1; add `EventManagedDBError` only if provisioning needs
   its own failure signal (optional — start without it, surface status in-UI).

### HTTP + audit

`api/internal/server/handler/databases.go` in the `/api` group:
- `GET/POST /api/apps/{id}/databases` (list / add — body `{engine}`), `DELETE …/databases/{dbid}`.
  POST returns the footprint warning + masked connection info; `audit.SetTarget(ctx, "managed_db",
  dbid)` + `Describe("provisioned {engine} for {slug}")`.

### UI (new feature `ui/src/features/databases/` — note: a `database/` folder already stubs here)

- **Databases tab / section** on app-detail: engine picker (Postgres = "free", others show the
  footprint warning before confirm), provisioned-DB list with masked connection string + a
  "reveal/copy" action, status pill, and delete.
- `ui/src/lib/api/databases.ts` (`useDatabases`, `useAddDatabase`, `useRemoveDatabase`); types in
  `types/api.ts` (`ManagedDatabase`, `DBEngine`).

### Tests

- `PostgresEngine` provision/deprovision (integration against `vac-db`: DB+role created, role
  scoped, password works, deprovision cleans up).
- Lazy start: first request brings up `vac-managed-mariadb`; second reuses it (no second
  container).
- Provision wires an env var + a backup config; app delete deprovisions engine-side objects.
- Handler: footprint warning in the add response; duplicate engine/db-name → 409.

### Acceptance

Adding a managed DB of the chosen engine creates an isolated database on a shared, lazily-started
per-engine instance, injects a connection env var, auto-creates a scheduled backup, and is torn
down cleanly on app delete — with no manual config and a footprint warning for non-Postgres
engines.

### Open questions to settle during D2

- `vac` role privileges: grant `CREATEDB`/`CREATEROLE` to the control-plane role, or run managed
  DDL as `postgres`? (Leaning: a dedicated `vac_provisioner` role with exactly those two grants.)
- `max_connections` budget on shared `vac-db` (control-plane pool is capped at 25; managed user
  DBs add connections). Document a budget; surface in the box-budget UI later.
- Redis multi-tenancy: logical DB index (`db0`/`db1`) vs key-prefix. (Leaning: logical index,
  capped at 16; warn when exhausted.)
- Major-version upgrades of a shared instance with user data — out of scope for v1; document the
  manual path.

### Files touched

`api/internal/db/migrations/00041_managed_databases.sql` (new), `api/internal/dbprovision/*` (new),
`api/internal/store/managed_dbs.go` (new), `api/internal/server/handler/databases.go` (new) +
route wiring, `api/internal/config/config.go` (`ManagedDBIsolated`), `api/main.go` (deprovision
hook on app delete), plus UI: `features/databases/*`, `lib/api/databases.ts`, `types/api.ts`.
**Touches `dockercli` (Up/Exec, already added in D1) and attaches engine containers to `vac-edge`
— flag the `vac-edge` attach to Track A's owner, but it reuses the existing
`NetworkConnect`/alias mechanism, not a pipeline rewrite.**

---

## D3 — `12` Add-on catalog (Grafana flagship)  *(effort M)*

**Goal:** a curated catalog of one-click templates deployed *as user apps* through the existing
pipeline. **Grafana is the flagship**; the same mechanism yields Umami, Plausible, n8n, Metabase,
Uptime-Kuma.

### The reframe (decision #7): a template is data, not a feature

A **template** = a `compose.yaml` + default env + a provisioning bundle + a footprint estimate +
an optional "depends on a managed DB?" flag. It deploys through the **existing pipeline** as a
normal user app → no control-plane bloat, the `<200 MB` claim is untouched (the add-on runs on the
user's box). **Guardrail:** templates are recipes, never per-add-on code in `vac-api`.

### The one real seam: a non-git "template" source

The pipeline always `git clone`s (`Pipeline.Run` → `cloneOrPull`). Templates have no repo. Two
options:

- **(A) Host templates in a `vac-templates` git repo** — zero pipeline change, but needs network
  + a maintained external repo.
- **(B) Embed templates and materialize them into the work dir, skipping clone** — offline-friendly,
  self-contained, versioned with the binary.

**Locked: (B).** `go:embed` a `templates/` tree into a new `api/internal/addon` package; on install,
copy the template's files into `{WorkDir}/{slug}/repo` and run the **existing** build/up/route
pipeline (the app's `build_kind=compose`). The seam is a small **"local source"** branch in the
clone step: when an app is flagged `source=template`, materialize embedded files instead of
cloning. *This is the only pipeline touch in all of Track D — coordinate with Track A at merge;*
it's an additive branch in the clone step, not a rewrite of build/up/health/route.

### Schema (migration `00042_addon_installs.sql`)

Minimal — a template install *is* an app, so reuse `apps`. Add only provenance:

```sql
ALTER TABLE apps ADD COLUMN source       TEXT NOT NULL DEFAULT 'git';  -- git | template
ALTER TABLE apps ADD COLUMN template_id  TEXT;                          -- e.g. 'grafana'
```

The catalog itself is embedded data (no table); installs are ordinary apps tagged with their
template id.

### Backend (new package `api/internal/addon`)

1. **Embedded registry** (`go:embed templates/*`): each template dir has `manifest.json` (id, name,
   description, category, footprint MB, `depends_on_db` engine?, default env) + `compose.yaml` +
   a `provisioning/` bundle. `addon.List()` and `addon.Get(id)` read the embed.
2. **Install flow** (`addon.Installer.Install(ctx, templateID, name)`): create an app
   (`source=template`, `template_id`, generated slug) → materialize files → if `depends_on_db`,
   call D2 to provision the DB and inject the connection env var **before** first deploy →
   `Enqueue` the deployment. Footprint warning returned to the UI **before** install.
3. **Clone-step branch** in `deploy` (the seam above): `source=template` → copy embedded files;
   else clone as today.

### Grafana specifics

- **"Charts about VAC" — lightweight path is the default** (`12` stub): Grafana reads VAC's
  `request_metrics` Postgres table directly via a provisioned SQL datasource — **no Prometheus
  process**, roughly half the footprint. The Prometheus path (Track B's `/metrics`, shipped) is an
  opt-in template variant for users who want a TSDB.
- **Provisioned JSON baked into the template:** datasources + dashboards under `provisioning/` so
  Grafana auto-loads them on first boot — zero manual setup.
- **"Dashboards from managed DBs"** (the wow feature) — nearly free once D2 exists: point a second
  Grafana SQL datasource at the app's managed Postgres/MariaDB (read-only connection string handed
  in from D2). This is why D3 sequences *after* D2.
- **Honest RAM:** Grafana idles ~100–150 MB; +Prometheus adds a few hundred. It's a user app (not
  control-plane), but show the footprint warning before install and **default to the lightweight
  path**.

### HTTP + audit

`api/internal/server/handler/addons.go` in the `/api` group:
- `GET /api/addons` (catalog list), `GET /api/addons/{id}` (detail + footprint),
  `POST /api/addons/{id}/install` → 202 with the new app DTO (then it streams deploy logs like any
  app). `audit.SetTarget(ctx, "app", appID)` + `Describe("installed add-on {id}")`.

### UI (new feature `ui/src/features/addons/`)

- **Add-ons catalog** surface (route `_app/addons.tsx` or a modal off the apps dashboard): cards
  per template (name, description, footprint badge, "depends on a managed DB" note), an install
  dialog showing the footprint warning + DB-dependency, then redirect to the new app's detail page
  to watch the deploy stream (reusing the existing live-deploy banner + WS log hooks).
- `ui/src/lib/api/addons.ts` (`useAddons`, `useAddon`, `useInstallAddon`); types in `types/api.ts`
  (`Addon`, `AddonInstallResult`). No new WebSocket — install reuses the deployment log stream.

### Tests

- Registry: `addon.List`/`Get` parse embedded manifests; Grafana template validates against the
  compose parser.
- Install: materializes files, creates an app `source=template`, provisions a DB when
  `depends_on_db`, enqueues a deploy.
- Clone-step branch: `source=template` skips git and copies embedded files (fake git asserts
  *not* called).
- Handler: catalog list/detail; install returns 202 + footprint; unknown id → 404.

### Acceptance

Enabling "Grafana" from the catalog deploys it as a stack through the normal pipeline, lands it
pre-provisioned with VAC dashboards (lightweight Postgres path by default), and — if managed DBs
exist — can query them, with a footprint warning shown before install.

### Files touched

`api/internal/db/migrations/00042_addon_installs.sql` (new), `api/internal/addon/*` (new, incl.
embedded `templates/grafana/*`), `api/internal/deploy/pipeline.go` (template-source branch in the
clone step — **the one Track-A-adjacent touch**), `api/internal/server/handler/addons.go` (new) +
routes, plus UI: `features/addons/*`, `lib/api/addons.ts`, `types/api.ts`, new route.

---

## Cross-track sync points (from `00`)

- **Migrations:** Track D owns `00040`–`00049` (uses `00040`/`00041`/`00042`). Latest on `main` is
  `00034`; the gap means no rebasing against A/B/C at merge.
- **`13` (Track B, shipped) ↔ `12` (this track):** D3's "charts about VAC" defaults to the
  **lightweight PG path** (Grafana → `request_metrics`), independent of Prometheus. Track B's
  frozen `vac_*` metric names are the contract for the *optional* Prometheus template variant —
  don't rename.
- **`08`/`09` (this track) ↔ Track A pipeline:** the **only** deploy-pipeline touch in all of
  Track D is D3's template-source branch in the clone step (additive). D1/D2 add a `dockercli.Exec`
  primitive and attach managed-DB containers to `vac-edge` via the existing
  `NetworkConnect`/alias mechanism — neither rewrites up/health/route. Flag both at the merge PR
  so A's owner sanity-checks the clone branch and the `vac-edge` attach.
- **D1 → D2 → D3 internal dependency:** D2's managed DBs are backed up by D1's engine; D3's
  managed-DB dashboards need D2. Build strictly in order.

## Strategy gate (don't ship ahead of demand)

Per the `09` stub and the `00` strategy gate: **build** Track D in parallel, but keep it behind
`VAC_MANAGED_SERVICES` (default off) and hidden from nav until Tracks A/B are demonstrably
trustworthy *and* a user actually asks. D is how VAC monetizes reliability — shipping it before the
reliability story is solid undercuts the pitch. The flag also guarantees the managed-services
goroutines (backup scheduler, lazy engine watchers) add **zero** idle footprint on boxes that
don't use them.

## Suggested commits (Conventional Commit, commitlint-compatible)

- `feat(backup): scheduled per-service backups with pluggable destinations` (D1)
- `feat(db): one-click managed databases, one shared instance per engine` (D2)
- `feat(addons): embedded template catalog with Grafana flagship` (D3)

Run `/code-review` + `/simplify` after each, and `/refresh-kb` at the end — Track D adds three
new packages (`backup`, `dbprovision`, `addon`), a `dockercli.Exec` primitive, and a
template-source branch in the deploy pipeline, so `architecture.md` and `deployment-flow.md` both
need regenerating. Log the template-source seam and the shared-vs-isolated managed-Postgres default
in `docs/deviations.md`.
