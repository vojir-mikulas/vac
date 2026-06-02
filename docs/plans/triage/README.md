# Triage — field notes from dogfooding (2026-06-02)

Raw notes / ideas / bugs / features captured while actually running VAC, then split
into grouped plans and **reconciled against the current source** so each item says
whether it's a real gap, a confirmed bug, or already built (and just needs UI surfacing).

> **Status of this folder vs. the others:**
> - [`../upcoming/`](../upcoming/) — committed *direction* (seeds we intend to flesh out & build).
> - [`../done/`](../done/) — shipped.
> - **`triage/` (here)** — raw observations not yet promoted. Once an item is verified and
>   prioritized, move it into `upcoming/` (or just fix it if it's a small bug).

Each note was checked against the code, so a few turned out to be **already implemented**
(base-domain UI, backup edit/download/S3 offsite, env masking). Those are marked `built —
verify/surface` rather than re-built. Confirmed defects are flagged `BUG`.

> **Want to run these in parallel?** See [`00-parallel-tracks.md`](00-parallel-tracks.md) — it
> groups every item into 6 concurrent tracks by subsystem ownership, names the 5 file-collision
> sync points, and calls out the one shared seam (Stage 0: expand the app DTO) that unblocks the
> rest. The four **confirmed bugs** are the critical path.

## Confirmed bugs (fast-track — these are real, found in code)

| Bug | Where | File |
|-----|-------|------|
| Changing a service's port + restart updates the DB only — container is **not** recreated, still runs old port | [port-handling.md](port-handling.md) | `store/services.go:151` (`SetServiceConfig` is DB-only; no redeploy) |
| Two managed DBs on one app **collide on `DATABASE_URL`** (both engines hardcode it; 2nd overwrites 1st) | [databases-and-backups.md](databases-and-backups.md) | `dbprovision/postgres.go`, `mariadb.go` (`EnvVarName()` → `"DATABASE_URL"`) |
| Addon apps (Grafana) bind port **3000**, which collides with `vac-api`'s default 3000 | [port-handling.md](port-handling.md) | `config/config.go:125`; addon ports come from template compose |
| Base-domain card may not reflect the **currently-configured** value / override path confusing | [domains.md](domains.md) | `instance.go:122` `PutBaseDomain` exists; reconcile is live (no restart) |

## Plans in this folder

| File | Covers (raw notes) | Headline |
|------|--------------------|----------|
| [addons.md](addons.md) | show-installed, hide DB/backup/build tabs for addons, addon-source settings, MariaDB addon, **uninstall**, brand icons | Make addon apps first-class & distinct from git apps |
| [databases-and-backups.md](databases-and-backups.md) | `DATABASE_URL` collision (BUG), Database tab, offsite/download backups, edit backup | DB & backup management — mostly built, needs surfacing + the collision fix |
| [domains.md](domains.md) | set default domain from UI, base-domain display (BUG), per-app domains | Domains — base-domain UI exists; fix display + per-app assignment |
| [app-detail-ux.md](app-detail-ux.md) | header start/stop/restart, shell into container, overview info panel, per-service stop+logs | App-detail control & overview polish |
| [build-cache-and-retention.md](build-cache-and-retention.md) | BuildKit cache, auto-prune, image/deploy retention | Wire up the prune/retention code that already exists but is never called |
| [port-handling.md](port-handling.md) | port change not applied (BUG), 3000 collision (BUG) | Port plumbing fixes |
| [security-and-metrics.md](security-and-metrics.md) | request metrics not working, ufw/fail2ban/traffic failing, security badge count | Why these read as "failing" (sandbox/config) + the fix |
| [env-vars.md](env-vars.md) | should encrypted env be recoverable? | Design note — currently masked + audit-logged reveal |

## How the reconciliation went (every note accounted for)

- **Already built, just verify/surface:** base-domain UI, custom-domain CRUD (global + per-app
  API), backup **edit** (`UpdateBackup`), backup **download** (`DownloadBackup`), **S3/offsite**
  destination, env-var masking + on-demand reveal. → details in each file.
- **Built but never wired:** image pruning (`ImageKeepCount` config + `ListImages`/`RemoveImage`
  exist, **never called**). → [build-cache-and-retention.md](build-cache-and-retention.md).
- **Missing entirely:** deployment retention, container shell, addon uninstall UX, per-DB env
  naming, header controls, overview panel, addon/git app distinction in the DTO.
- **Confirmed bugs:** port-change-not-applied, `DATABASE_URL` collision, 3000 collision.
- **Expected-given-design (needs better UX, not a "fix"):** security host reads (fail2ban/ufw)
  degrade to "not detected" because the control plane is deliberately sandboxed and runs
  read-only as non-root → can't read the fail2ban socket / ufw without a privileged helper.
</content>
</invoke>
