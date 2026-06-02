# P1 — Addons & Managed Services (detailed implementation plan)

**Track:** [P1 in 00-parallel-tracks.md](00-parallel-tracks.md) · **Source notes:**
[addons.md](addons.md), [databases-and-backups.md](databases-and-backups.md) · **Status:** ready
to build · **Date:** 2026-06-02

Sequenced, implementation-ready breakdown of Track P1. Items are ordered because they share the
same packages (`addon`, `dbprovision`, `backup`) and the same app-detail/Settings UI surface.
The one **confirmed bug** (P1.1) is the spine; the rest is surfacing capabilities that already
exist in the backend.

**Owns:** `internal/addon`, `internal/dbprovision`, `internal/backup`, the
databases/backups/addons handlers, and the addon-catalog + app-tabs + app-Settings UI.

> **Migration budget:** latest applied is `00042`; upcoming Track E reserved `00050–00059`. P1's
> only schema change (P1.1) claims **`00060`**.

---

## Prerequisite — Stage 0 (the slice P1 depends on)

Stage 0 in the parallel-tracks doc is a shared seam with P3. P1 needs **two of its fields** on the
app DTO before P1.2/P1.3 can render. If Stage 0 hasn't landed, do this sub-slice first; it is pure
DTO plumbing — no schema change (`source`/`template_id` already exist on `store.App`,
`store/apps.go:43-46`).

**File:** `api/internal/server/handler/apps.go`

- `appDTO` (lines 68-81) currently exposes: `id, name, slug, git_url, git_branch, compose_file,
  build_kind, build_config, status, mem_limit_mb, created_at, updated_at`. It does **not** expose
  `source` or `template_id` — confirmed. This is the root cause behind P1.2/P1.3: the UI cannot
  tell a Grafana addon from a git app.
- Add to `appDTO`:
  - `source string` — `"git"` | `"template"` (copy `store.App.Source`)
  - `template_id *string` — copy `store.App.TemplateID`
  - `template_name *string`, `template_icon *string` — **resolved** from the addon registry by
    `template_id` (nil for git apps). Resolution needs the `AddonCatalog.Get(id)` already wired
    into the handlers (`handler/addons.go:18-21`); pass the catalog into the apps handler (or a
    small `func(id string) (name, icon string, ok bool)`).
- Update `toAppDTO()` (lines 83-102) to populate them.
- TS type: add `source`, `template_id`, `template_name`, `template_icon` to `App` in
  `ui/src/types/api.ts:21-35`.

> **Sync point #1** — `appDTO` is also edited by P3 (overview panel). Land Stage 0 once, up front;
> don't let two tracks edit `appDTO` independently.

---

## P1.1 — `DATABASE_URL` collision (BUG) · Effort: M · Migration `00060`

**The defect.** Both engines hardcode the connection env var:
`dbprovision/postgres.go:101` and `mariadb.go:120` both return `EnvVarName() → "DATABASE_URL"`.
The provisioner injects it via `UpsertEnvVar(app.ID, row.EnvVarName, sealed, true)`
(`provisioner.go:144`), and `UpsertEnvVar` is an upsert keyed on `(app_id, key)`
(`store/env_vars.go:78-85`). Provision a second managed DB on one app and its `DATABASE_URL`
**silently overwrites** the first — the app only ever sees the last-provisioned connection string.

A related latent issue: the backup config is keyed `(app_id, service_name)` and `service_name` is
`eng.BackupContainer()` (`provisioner.go:151-160`). Two DBs of the **same engine** on one app
collide there too (both `vac-db`, or both `vac-mariadb`) — the second `CreateBackupConfig` returns
`ErrConflict` and is silently swallowed (`provisioner.go:160`). Cross-engine is fine (`vac-db` vs
`vac-mariadb`); same-engine is not.

**Fix — let the user name the binding, default `DATABASE_URL`, reject duplicates.**

1. **Migration `00060_managed_db_binding.sql`** — the `env_var_name` column already exists
   (`managed_databases`, migration `00041`, default `'DATABASE_URL'`), so the storage is there. Add
   a uniqueness guard so a duplicate binding on one app is a hard error, not a silent overwrite:
   ```sql
   -- +goose Up
   CREATE UNIQUE INDEX managed_databases_app_env_var_name_uniq
       ON managed_databases (app_id, env_var_name);
   -- +goose Down
   DROP INDEX managed_databases_app_env_var_name_uniq;
   ```
2. **Provisioner `Add` takes a binding name.** `Add(ctx, app, engine)` (`provisioner.go:101-127`)
   currently stores `eng.EnvVarName()` (line 121). Change the signature to
   `Add(ctx, app, engine, envVarName string)`:
   - If `envVarName == ""`, default to `eng.EnvVarName()` (`"DATABASE_URL"`) **only when the app
     has no managed DB yet**; otherwise require an explicit non-default name (or auto-suffix —
     see step 4) so the second DB can't claim `DATABASE_URL` again.
   - Validate the name is a legal env-var identifier (`^[A-Z_][A-Z0-9_]*$`).
   - Store it in `managed_databases.env_var_name` (line 121) and inject it at line 144.
3. **Pre-flight duplicate check.** Before insert, call `ListManagedDatabasesForApp` and reject if
   the chosen `env_var_name` is already taken → return a typed `ErrConflict` so the handler maps
   it to `409` with a clear message ("DATABASE_URL is already used by the postgres database; pick
   another binding name"). The unique index from step 1 is the backstop.
4. **Optional convenience — auto-suffix.** When the user doesn't supply a name and `DATABASE_URL`
   is taken, derive `DATABASE_URL_<ENGINE>` (e.g. `DATABASE_URL_MARIADB`) and bump to `_2`, `_3`
   on further collisions. Keep `DATABASE_URL` for the first DB so existing single-DB apps are
   unchanged.
5. **Fix the backup-config collision.** Key the backup config on something unique per DB. Cleanest:
   pass the managed-DB id (or `env_var_name`) into the backup `service_name` slot so two same-engine
   DBs don't share one config — OR document that same-engine-twice shares one backup container and
   make that explicit instead of a swallowed `ErrConflict`. At minimum, stop silently dropping the
   second config (`provisioner.go:160`): surface it as a warning the DB DTO can show.

**Handler & API.**
- `AddDatabase` (`handler/databases.go:95-138`) request gains an optional `env_var_name`:
  `{"engine": "postgres", "env_var_name": "ANALYTICS_DB_URL"}`. Empty = backend default/auto-suffix.
- Map the new `ErrConflict` path → `409` with the binding-name message.
- `managedDatabaseDTO` already returns `env_var_name` (`databases.go:27-55`) — no DTO change.

**UI.** In the Add-database dialog (P1.5's Database tab), add an optional "Bind to env var"
field defaulting to `DATABASE_URL`, disabled/auto-filled to the suffixed suggestion when
`DATABASE_URL` is already present on the app. Show the resulting var name on each DB row.

**Tests.** `provisioner_test.go` — add: second `Add` with default name on an app that already has
`DATABASE_URL` → `ErrConflict`; explicit distinct name → both env vars present; auto-suffix path
yields `DATABASE_URL_MARIADB`. Migration up/down round-trips.

---

## P1.2 — Addon distinction UI · Effort: S · (needs Stage 0)

With `source`/`template_id`/`template_name` on the DTO, make addon apps look and behave like
addons, not half-configured git apps.

**File:** `ui/src/routes/_app/apps/$appId.tsx` (tab assembly, lines 20-47).

- **Hide DB/Backups/Build tabs for pure addons.** Today managed tabs are gated only on
  `instance?.managed_services` (lines 45-47). Add: when `app.source === 'template'` **and** the app
  has no managed DB, drop `MANAGED_TABS` (Databases, Backups). Grafana depends on no DB
  (`manifest.json` `depends_on_db: ""`), so it should show neither. An addon that *did* provision a
  DB (future MariaDB-backed template) keeps them.
  - "has a managed DB" = the databases query returns ≥1 row; reuse the existing databases query
    (the Databases tab already calls it) or expose a `has_managed_db bool` on the app DTO to avoid
    a second fetch. Prefer the DTO flag — cheap in `toAppDTO` via a count.
- **Installed status in the catalog.** `ui/src/features/addons/addons-page.tsx` (cards lines 40-72,
  `InstallDialog` lines 75-120). Cross-reference the catalog (`useAddons`) against installed apps
  (`useApps` filtered to `source === 'template'`, matched by `template_id`). For an installed
  template, render the card as **Installed → "Open"** (navigate to `/apps/$appId/overview`) plus the
  Uninstall affordance from P1.4, instead of the Install dialog.

**File:** `ui/src/features/app-detail/settings-tab.tsx` (Source section lines 140-181, Auto-Deploy
line 183, Build lines 185-195).

- **Addon Settings = read-only "Installed from {template}".** When `app.source === 'template'`,
  replace the Source inputs (repo URL 148, branch 156, compose 164, deploy-key card 179), the
  Auto-Deploy section (183), and the Build picker (185-195) with a single read-only panel:
  brand icon + "Installed from {template_name}". Keep General (name), Runtime (RAM), Portability,
  and Danger zone. Addon apps have empty `git_url`/`git_branch` and `build_kind="compose"`
  (`store/apps.go` `CreateTemplateApp`), so these inputs are meaningless for them today.

> **Sync point #3** — P4.2 adds a per-app Domains section to this same Settings screen. Additive;
> coordinate component order (Domains belongs above Danger zone, after Runtime).

---

## P1.3 — MariaDB addon catalog entry · Effort: S–M

Reuse the addon → provision path. MariaDB is already a registered engine
(`dbprovision/mariadb.go`, `Name() → "mariadb"`), and the installer already provisions a DB when a
template declares one: `installer.go:104-110` calls `dbProv.Add(ctx, app, engine)` whenever
`tmpl.DependsOnDB != ""`.

**Two readings of "MariaDB addon" — pick deliberately:**
- **(a) A managed MariaDB exposed in the addons catalog** (most consistent with the note). The
  addon's job is to provision a managed MariaDB and bind it. But the install flow attaches the DB to
  a *new app* (`installer.go` creates an app first). A bare database isn't an app with a service —
  so a "MariaDB addon" really means "a starter app that ships with a managed MariaDB", or it means
  surfacing **"Add MariaDB"** in the per-app Database tab (P1.5) rather than the global catalog.
  Recommend: surface managed MariaDB in the **Database tab** (P1.5) as the primary path, and only
  add a catalog tile if there's a meaningful app to wrap it in.
- **(b) A template app that depends on MariaDB.** Add a new template dir
  `addon/templates/<name>/` with `manifest.json` `depends_on_db: "mariadb"`; the existing installer
  provisions it automatically. No installer changes needed.

**If adding a template** (`addon/registry.go` Template struct lines 24-35; templates embedded via
`//go:embed templates`, registry.go:19):
- Create `addon/templates/<id>/manifest.json` + `compose.yaml`. `id` must match the directory name
  (validated in `NewRegistry`, registry.go:46-75).
- Set `depends_on_db: "mariadb"` to trigger auto-provision.
- **Mind the binding:** the auto-provisioned DB will want `DATABASE_URL` — fine for a single DB, and
  P1.1 makes the multi-DB case safe.

> **Sync point #4** — editing the addon registry/templates also happens in P2.2 (Grafana port fix).
> One owner for template edits, or sequence P2.2 → P1.3.

---

## P1.4 — Uninstall addon · Effort: M

**Good news: backend teardown already exists.** `DeleteApp` (`handler/apps.go:359-380`) already:
deprovisions every managed DB engine-side via `dbDeprov.DeprovisionApp` (line 365 →
`provisioner.go:206-225`), tears down Caddy routes + `vac-edge` attachments (line 369), then
deletes the app; the cascade drops `managed_databases`, `env_vars`, and `backup_configs` rows.
So "uninstall" is **mostly a UI affordance + addon-aware confirm**, not new teardown logic.

**Gaps to close:**
1. **Verify named-volume cleanup.** Grafana ships `grafana-data:/var/lib/grafana`
   (`templates/grafana/compose.yaml`). Confirm whether `DeleteApp`/`proxyTeardown` runs
   `docker compose down -v` (or equivalent) so the **volume** is removed, not just containers. If
   not, the note's "deletes Grafana and its data" promise is unmet — add volume teardown to the
   compose-down path. **This is the one place uninstall may need backend work — verify first.**
2. **Addon-aware confirm copy.** Generic delete says "delete app". For `source === 'template'`,
   the confirm should read "This uninstalls {template_name} and permanently deletes its data
   (database + volumes). This cannot be undone." List what gets removed (the managed DB if any).
3. **Surface Uninstall on the catalog Installed card** (P1.2) and keep the Danger-zone delete in
   Settings — both call the same `DELETE /apps/{id}`.

No new endpoint; this is `DeleteApp` + UI. Add a UI test that the Installed card's Uninstall
triggers the delete mutation and navigates back to the catalog.

---

## P1.5 — Database tab + backup UX + brand icons · Effort: M

Surface the substantial backend that already exists; almost no new Go.

**Database tab** (`ui/src/routes/_app/apps/$appId/databases.tsx` — `DatabasesTab` already exists and
is gated by managed_services). Make it the home for managed DBs:
- List managed DBs with engine, status (`provisioning`/`ready`/`error` — `managedDatabaseDTO.status`,
  `databases.go:27-55`), and the injected env var (`env_var_name`). Poll while `provisioning`
  (`AddDatabase` returns `202`; status flips async).
- Add-database dialog → `POST /apps/{id}/databases` with `{engine, env_var_name?}` (P1.1). Show the
  shared-instance warning the handler already returns (`databases.go:87-90`, e.g. MariaDB ~150 MB
  first use).
- Delete → `DELETE /apps/{id}/databases/{dbid}` (`RemoveDatabase`, `databases.go:142-158`,
  `prov.Remove` drops engine objects + env var + backup config + row, `provisioner.go:180-200`).
- Show each DB's backups inline (the backups query is per-app + service; a managed DB's
  `service_name` is its `BackupContainer()`).

**Backup UX** — all backend pieces exist; this is surfacing (`databases-and-backups.md` §3-4):
- **Offsite (S3/B2) is built** (`backup/destination.go:34-59`, `backup/s3.go` full SigV4). In the
  backup form, make local-vs-S3 a clear choice; when S3, collect `endpoint/region/bucket/access/
  secret/use_ssl/prefix` (`backup.S3Config`, `s3.go:24-32`). Creds are sealed and never returned.
- **Recent runs + Download are built.** `ListBackupRuns` (`backups.go:289-309`) and `DownloadBackup`
  (`backups.go:312-345`, streams the artifact for a `success` run). Render a runs table:
  started/finished, status, size (`backupRunDTO`, `backups.go:41-50`), Download button → the
  download endpoint.
- **Edit is built.** `UpdateBackup` (`backups.go:203-245`, `PUT /apps/{id}/backups/{cid}`) preserves
  sealed S3 creds when `destination=="s3"` and the request omits `s3` (lines 223-231). Add an Edit
  action per config — service name is immutable (line 217).
- Default new managed-DB backups to prompt for an offsite destination (the note's "storing on the
  VPS isn't good practice").

**Brand icons (react-icons)** — `addons.md` §6. **react-icons is NOT currently a dependency** (the
UI uses lucide-react exclusively, `ui/package.json:31`). Two options:
- **Add `react-icons`** (`pnpm add react-icons`) for brand glyphs (`SiGrafana`, `SiMariadb`,
  `SiPostgresql`) with brand color. Add an `icon` field to the addon manifest/Template
  (`addon/registry.go` Template struct lines 24-35) so the icon name travels in the DTO
  (`template_icon`, Stage 0), and map it to the component in the UI.
- **Or** keep lucide-only and ship a small brand-icon map in the UI keyed by `template_id`/engine,
  avoiding a new dep. Decide with the maintainer — adding react-icons is the note's literal ask but
  pulls in a large icon set; tree-shaking via the `react-icons/si` subpath import keeps it lean.
- Render the brand icon in catalog cards (`addons-page.tsx:51-72`, currently a generic `Blocks`
  icon at line 56) and on the app avatar (`apps-dashboard.tsx:264-266`, currently the
  first-letter avatar) for template apps; fall back to the letter avatar for git apps.

---

## Sequencing & sync points

```
Stage 0 (DTO: source/template_id/name/icon)  ──┐
                                               ├─ P1.2 distinction UI ─┐
P1.1 DATABASE_URL collision (BUG, migr 00060) ─┤                       ├─ P1.5 DB tab + backups + icons
                                               ├─ P1.3 MariaDB catalog ┘
                                               └─ P1.4 uninstall
```

- **P1.1 first** (it's the bug, and P1.5's add-DB dialog/binding field depends on it).
- **Stage 0 before P1.2/P1.3** (DTO fields).
- P1.2, P1.3, P1.4 are independent of each other once Stage 0 + P1.1 land; P1.5 ties them together.
- **Sync points (from 00-parallel-tracks.md):** #1 app DTO (with P3 — Stage 0 resolves it), #3 app
  Settings UI (with P4.2 — coordinate section order), #4 addon templates (with P2.2 — one owner /
  sequence P2.2 → P1.3).
- **Migration:** P1.1 claims **`00060`**. No other P1 item needs one.

## Acceptance (combined)

- Provisioning a second managed DB yields a **distinct, documented** env var — no silent overwrite;
  a duplicate binding is a `409`, not data loss.
- The addons catalog distinguishes **Installed vs available**; installed cards offer **Open** +
  **Uninstall**; uninstall removes the app **and its data** (DB + volumes), behind a clear confirm.
- An addon app's Settings reads **"Installed from {template}"** — no repo/branch/build/autodeploy
  inputs, and no Database/Backups tabs unless it has a managed DB.
- A managed **MariaDB** is installable (catalog tile and/or Database-tab "Add MariaDB").
- The **Database tab** lists managed DBs (engine, status, env var) with inline backups; the backup
  UI lets you pick local **or** S3/B2, see recent runs with size + **Download**, and **Edit** an
  existing config.
- Catalog + app cards show **brand icons** for known templates; git apps keep the letter avatar.

## Verification checklist

- [ ] `make test` (Go race + vitest), `make lint`, `make typecheck`.
- [ ] Migration `00060` up/down round-trips; existing single-DB apps keep `DATABASE_URL`.
- [ ] **Manually confirm volume teardown on uninstall** (P1.4 gap #1) — Grafana's `grafana-data`
      volume is gone after delete.
- [ ] Run `/code-review` (correctness) and `/simplify` (cleanup) before marking done.
- [ ] Regenerate affected KB (`docs/kb/architecture.md`, `deployment-flow.md`) via `/refresh-kb` if
      the dbprovision/addon boundaries shift.
</content>
</invoke>
