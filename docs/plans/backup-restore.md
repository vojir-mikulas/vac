# Backup Restore — design sketch

VAC creates and schedules backups (local + S3) and lets you download an artifact, but there's no
way to **put one back**. This adds the precise inverse of the dump pipeline: read a recorded run's
artifact from its destination, stream it into the target container, and replay it with the engine's
restore CLI — gated like the destructive action it is (typed confirmation + step-up 2FA).

Status: **implemented** (migration `00068_backup_restores.sql`; `backup.Restorer`,
`dockercli.ExecStdin`, `dbprovision.RestoreCommand`/`MatchBackupCommand`, the
`restore_finished` notify event, and the `POST .../restore` + `GET .../restores`
endpoints behind `RequireStepUp`).

## Why

A backup you can't restore is a false sense of safety. The engine already produces dumps
(`exec → stream stdout → destination → record run → prune → notify`) and the artifact is readable
(`DownloadBackup` streams it to the browser). The operator's only restore path today is: download
the `.dump`, SSH in, and hand-feed it to `psql`/`mariadb` themselves — exactly the manual toil VAC
exists to remove. Restore is the missing half of the same code path, mostly reusing primitives that
already exist.

## What already exists (don't rebuild)

- **Artifact read-back is already implemented.** `Destination.Open(ctx, key)` returns an
  `io.ReadCloser` for a stored artifact on both backends — `LocalDestination.Open`
  (`backup/destination.go:105`, an `os.Open`) and `S3Destination.Open`
  (`backup/s3.go:138`, a signed GET). The download handler already consumes it
  (`handler/backups.go:426`).
- **Container resolution (managed-DB vs app-service) is solved.** `Engine.resolveContainer`
  (`backup/engine.go:130`) resolves a config's `service_name` to a container, falling back to
  treating it as a literal container name when the service lookup returns `ErrNotFound` — so a
  managed DB on a shared engine (`vac-db`, `vac-mariadb`) resolves the same as an app service.
  Restore reuses this verbatim (see deviations.md "D1 — Managed-DB backups exec into a container by
  name").
- **Run lookup + ownership check.** `store.GetBackupRun` (`store/backups.go:323`) plus the
  `cfg.AppID != appID` guard (`handler/backups.go:417`) already gate "this run belongs to this app
  and has a downloadable artifact" (`run.Status == "success" && run.ArtifactKey != nil`,
  `handler/backups.go:412`).
- **Step-up 2FA for destructive routes.** `middleware.RequireStepUp` already fronts delete-app and
  reset-instance (`server.go:204,257,297`). The client auto-prompts: a `403 step_up_required`
  triggers the global `StepUpProvider` and transparently retries (`lib/api/client.ts:110`). Wiring
  restore behind the same middleware gets the whole flow for free.
- **Typed-phrase confirmation pattern.** The instance-reset dialog requires typing a literal phrase
  before the destructive button enables (`danger-zone-section.tsx:168`,
  `disabled={phrase !== RESET_PHRASE}`). Restore mirrors it (type the app/service name).
- **Engine restore knowledge lives next to the dump knowledge.** Each `dbprovision.Engine` already
  owns `DefaultBackupCommand` + `BackupContainer` (`dbprovision/engine.go:48-53`); Postgres dumps
  with `pg_dump -U vac <db>` (`postgres.go:124`) and MariaDB with `mariadb-dump <db>` reading
  `/root/.my.cnf` (`mariadb.go:174`). The restore command is the obvious counterpart
  (`psql`/`mariadb` reading from stdin) and belongs on the same interface.
- **Notify + audit.** `Notifier.BackupFailed` (`backup/engine.go:30`) and `audit.SetTarget/Describe`
  (`handler/backups.go:286`) are already used by the backup path — restore records the same way.

## Key technical realities (read before building)

- **Restore needs stdin, not stdout.** The dump path streams the container's stdout *out*
  (`Compose.Exec`, `compose.go:199`). Restore must stream the artifact *in* — `docker exec -i` with
  the dump piped to the container's stdin. `Compose.Exec` has no stdin parameter; `ExecInteractive`
  (`exec_interactive.go:27`) allocates a PTY, which is wrong for a non-interactive byte pipe. **A
  new primitive is required**: `ExecStdin(ctx, containerID, cmd, stdin io.Reader) error` — the
  mirror of `Exec`, attaching a reader to the child's stdin and capturing stderr for the error.
- **Restore is destructive and never atomic mid-stream.** `psql`/`mariadb` apply statements as they
  arrive; a failure halfway leaves the database partially overwritten. This is unlike a deploy
  (which *never* tears down the running stack on failure — see CLAUDE.md). There is no rollback —
  the honest mitigation is: the restore command itself recreates a clean target
  (`DROP DATABASE … CREATE DATABASE …` / `pg_dump`'s `--clean` semantics handled by the dump), and
  the operator is warned in the strongest terms before confirming.
- **vac-api is off vac-edge — but restore doesn't need it.** Like the dump, restore goes through
  `docker exec` into the target container, not a network connection to it. The invariant holds
  unchanged; no new reachability is introduced.
- **The dump format dictates the restore command.** Plain-SQL dumps (`pg_dump` default,
  `mariadb-dump`) restore with `psql`/`mariadb` reading SQL from stdin. Custom-format dumps
  (`pg_dump -Fc`) need `pg_restore`. VAC's default commands produce **plain SQL**
  (`postgres.go:124`, `mariadb.go:174`), so the default restore is `psql`/`mariadb`. For a
  user-authored custom `Command`, the matching restore is not knowable from the config — see Scope.
- **Long dumps run detached; restore should too.** `RunBackup` runs off-request with a 30-min
  timeout (`handler/backups.go:368`). Restore mirrors this: it can't block the request, and it needs
  the same run-style lifecycle row so the UI can show progress.

## Scope decisions (the important part)

1. **Restore only what VAC produced with a known engine command.** A managed DB (Postgres/MariaDB)
   or an app service whose backup `Command` is one of VAC's defaults has a known inverse — restore
   it. For a hand-authored `Command` VAC can't infer the restore command; surface "download and
   restore manually" rather than guess and corrupt data. (v1 covers the common, auto-configured
   case; custom-restore-command config is a later refinement.)
2. **Restore a chosen run, not just the latest.** The UI already lists run history with per-run
   download (`backups-tab.tsx:226`); restore hangs off the same rows so the operator can pick *which*
   point-in-time to roll back to.
3. **Destructive-action gating, mirrored exactly.** `RequireStepUp` middleware (reuse, don't invent)
   **plus** a typed-name confirmation in the dialog (mirror the reset phrase). Both, because the
   damage is irreversible and silent.
4. **No new destination code.** Reuse `Destination.Open`; restore is destination-agnostic for the
   same reason backup is.
5. **One restore at a time per config.** A concurrent second restore (or a restore racing a
   scheduled backup) into the same DB is incoherent. Guard with the run-lifecycle row (refuse if a
   restore for this config is already `running`).

## Phase 1 — Backend: the restore primitive + engine

New `ExecStdin` on `dockercli.Compose` (the stdin mirror of `Exec`, `compose.go:199`): `docker exec
-i {container} sh -c {cmd}` with `stdin` wired to the passed reader and stderr captured for the
error. No PTY.

New `backup.Restorer` (sibling to `Engine`, same package — shares `resolveContainer`, `Destination`,
the run-row store interface):

- `Restore(ctx, cfg store.BackupConfig, runID string) error`:
  1. Load the run; assert `status == "success"` and `artifact_key != nil` (reuse the
     `DownloadBackup` checks).
  2. Resolve the restore command for `cfg` (decision #1): map the stored `Command` to its inverse
     via the engine recipe — a new `Engine.RestoreCommand(dbName) string` on
     `dbprovision.Engine` (`engine.go:30`), e.g. Postgres → `psql -U vac -d <db>`, MariaDB →
     `mariadb <db>` (reads `/root/.my.cnf`). Refuse (typed error) if the command isn't a recognized
     default.
  3. `resolveContainer` → container id/name (`engine.go:130`, unchanged).
  4. `dest.Open(ctx, *run.ArtifactKey)` → reader.
  5. `ExecStdin(ctx, container, restoreCmd, reader)` — stream the artifact into the engine CLI.
  6. Record outcome on a **restore run row** (see store) and fire a notify event on failure.

Store: a `backup_restores` table (migration, mirror `backup_runs` in `00040_managed_backups.sql`):
`id`, `config_id` (FK, cascade), `source_run_id`, `started_at`, `finished_at`, `status`
(`running|success|failed`), `error`. Methods `CreateRestoreRun`, `FinishRestoreRun`,
`ListRestoreRuns(configID)`, `LatestRestoreRun(configID)` — copy the `backup_runs` shapes
(`store/backups.go:297-371`).

Notify: add `notify.Dispatcher.RestoreFinished(appName, appID, service string, ok bool)` next to
`BackupFailed`; one event type + toggle key + two ~10-line renderers (same shape the
volume-alerts/backup-failed events use).

API (gated by `cfg.ManagedServices`, behind `RequireStepUp`, registered next to the backup routes at
`server.go:344`):

- `POST /api/apps/{id}/backups/runs/{rid}/restore` → 202; runs detached with a 30-min timeout like
  `RunBackup` (`handler/backups.go:368`); ownership + downloadable-artifact checks reused from
  `DownloadBackup`; `audit.SetTarget("backup", …)` + `audit.Describe("restored <slug>/<service> from
  run <rid>")`.
- `GET /api/apps/{id}/backups/{cid}/restores` → restore-run history for the progress view.

## Phase 2 — UI (features/backups + app-detail backups tab)

In `RunHistory` (`backups-tab.tsx:226`), beside the existing **Download** link on each successful
run, add a **Restore** action:

- Opens an `AlertDialog` (reuse the reset-dialog pattern, `danger-zone-section.tsx:144`): red framing,
  a blunt "this overwrites the current data in `{service}` and cannot be undone", and an `Input` that
  must equal the service name before the destructive button enables (`disabled={phrase !== service}`).
- On confirm → `useRestoreBackup(appId).mutate({ runId })`. A `403 step_up_required` is handled
  automatically by the existing `StepUpProvider` (`lib/api/client.ts:110`) — no per-call 2FA code.
- After 202, poll `GET .../restores` (or reuse the run-history query invalidation) to show a
  `running → success/failed` `StatusPill`, same as backup runs.
- Hide/disable Restore for runs whose config has a non-default custom `Command` (decision #1) with a
  tooltip pointing at manual download.

API client: `lib/api/backups.ts` — add `restore(appId, runId)` + `useRestoreBackup`, and a
`restores(appId, cid)` query. Types: add `RestoreRun` to `types/api.ts`. New i18n keys under the
`app-detail` `backups.*` namespace (`restore`, `restore.confirmTitle`, `restore.confirmDescription`,
`restore.typeToConfirm`, …).

## Out of scope (explicitly)

- **Custom restore commands** for hand-authored backup `Command`s — VAC can't infer the inverse;
  manual download stays the answer. A future `restore_command` config column could close this.
- **Cross-app / cross-service restore** ("restore app A's dump into app B"). Restore targets the
  run's own config only; redirection invites footguns.
- **Point-in-time / WAL replay.** VAC stores discrete logical dumps; restore replays one dump, full
  stop. No incremental/PITR.
- **Custom-format (`pg_dump -Fc`) + `pg_restore`** — VAC's defaults are plain SQL; if a user opts
  into `-Fc` themselves, that's the custom-command case (out of scope above).
- **Volume / file restore** — Track D backups are logical DB dumps; restoring raw volume contents is
  a different feature entirely.
- **Automatic rollback on partial failure** — there is none (see realities); the dialog warns and the
  restore command recreates a clean target.

## Rough size

- Phase 1: 1 `ExecStdin` primitive, 1 `Restorer` (heavy reuse of `Engine` helpers), 1
  `RestoreCommand` per engine recipe, 1 migration + ~4 store methods (cloned from `backup_runs`), 1
  notify event + 2 renderers, 2 endpoints behind `RequireStepUp`. Medium — the new stdin exec path
  and the destructive-gating wiring are the real work; everything else is mirrored.
- Phase 2: 1 dialog + 1 action in `RunHistory`, 1 mutation + 1 query + 1 type, a handful of i18n
  keys. Small — step-up is already automatic.

## Build order

1. `dockercli.ExecStdin` (+ an integration test alongside `exec_integration_test.go`).
2. `Engine.RestoreCommand` for Postgres + MariaDB (+ recognize-default mapping; refuse otherwise).
3. `backup_restores` migration + store methods (clone `backup_runs`).
4. `backup.Restorer.Restore` reusing `resolveContainer` / `Destination.Open` / run-row store.
5. `notify` event + 2 renderers.
6. `POST .../restore` + `GET .../restores` behind `RequireStepUp`, gated by `ManagedServices`.
7. UI: restore dialog (typed-name confirm) + mutation/query/types + i18n in the backups tab.
8. `/code-review` + `/simplify`; `/refresh-kb` (new endpoints + the `ExecStdin` primitive touch
   `architecture.md`/`deployment-flow.md`).

## Verification

- A managed Postgres DB: take a backup, drop a table, restore the run → the table is back; the
  restore-run row shows `success` and a notify fires.
- A MariaDB managed DB: same round-trip via `vac-mariadb` (validates the container-name fallback
  path).
- Step-up: with TOTP enabled, the first restore prompts for a code; a second within `StepUpTTL`
  doesn't. Wrong typed name keeps the button disabled (`danger-zone` parity).
- A run with a non-default custom `Command`: Restore is disabled with the manual-download hint.
- A failed/in-progress run: no Restore affordance (mirrors the download guard).
- `make lint typecheck test` clean; `make test-integration` covers `ExecStdin` against a real
  container.
