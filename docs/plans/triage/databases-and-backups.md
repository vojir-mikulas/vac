# Managed databases & backups

**Status:** triage · **Effort:** S–M (one real bug; the rest is surfacing what exists)

## 1. BUG — two databases on one app collide on `DATABASE_URL`

Both engines hardcode the connection var: `dbprovision/postgres.go` and `mariadb.go` return
`EnvVarName() → "DATABASE_URL"`, and the provisioner injects it via `UpsertEnvVar`
(`dbprovision/provisioner.go:144`). Provision a second DB and it **overwrites** the first —
no collision detection, no per-DB naming.

**Fix options:**
- Give each managed DB a stable, unique env var name: keep `DATABASE_URL` for the first, then
  `DATABASE_URL_2` / engine-or-name-suffixed (`DATABASE_URL_MARIADB`, or user-supplied alias).
- Let the user **name the binding** at provision time (default `DATABASE_URL`), and reject /
  warn on duplicates before injecting.
- Also fix the shared-`BackupContainer` idempotency assumption (`provisioner.go:151-160`) so
  two DBs don't silently share one backup config.

**Effort:** M. This is the highest-value fix in this file.

## 2. Manage databases from a dedicated "Database" tab

Backend already has the endpoints — `server/handler/databases.go` (list/get/create/delete) and
the provisioner. The note is that management is scattered. → Build a **Database tab** on the app
detail that lists managed DBs (engine, status `provisioning`/`ready`, the injected env var),
shows their **backups** inline, and offers create/delete. **M**

## 3. Backups — "smarter, send offsite, download" (mostly already built — verify & surface)

Reality check against the code:
- **Offsite already exists:** `backup/destination.go:38` supports `local` (VPS disk) **and**
  `s3` (S3-compatible — B2 etc.), credentials sealed with the master key.
- **Download already exists:** `server/handler/backups.go:311` `DownloadBackup` streams a
  successful run's artifact.
- **User-provided command already exists:** `backup_configs.command` runs an arbitrary
  `docker exec` per service (`backup/engine.go:66`).

So the note ("storing on the VPS isn't good practice; how do I show recent backups + download")
is **largely solved** — but the UI likely doesn't surface it well. → Action is UX, not backend:
default new managed-DB backups to a prompt for an S3/B2 destination, list recent runs with
status + size + a Download button, and make the offsite option prominent. **S–M**

## 4. Backups — "no edit backup" (already built — verify)

`server/handler/backups.go:203` `UpdateBackup` already supports editing destination, schedule,
command, and S3 creds (preserves existing creds if not resent). → If the UI has no edit button,
this is a **missing UI control**, not missing backend. Add an Edit action to each backup config. **S**

## Acceptance sketch

- Provisioning a second DB yields a distinct, documented env var (no silent overwrite).
- App has a Database tab listing managed DBs + their backups.
- Backup UI: pick local **or** S3/B2, see recent runs with Download, and Edit an existing config.
</content>
