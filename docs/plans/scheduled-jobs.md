# Scheduled Jobs — cron-as-a-service for an app

Let an operator run a command on a schedule against one of an app's services — DB cleanup,
report generation, cache warm, `rails db:migrate`, a nightly `curl` to a webhook. VAC already
runs *internal* timers (backup scheduler, retention pruner) but exposes **no user-facing cron**.
This adds one: a `scheduled_jobs` config + a `job_runs` history + a single scheduler goroutine
modelled exactly on `backup/scheduler.go`.

Status: **planned** (not started).

Smallest-useful-first: **(1) exec-into-running-container jobs on a simple interval/daily/weekly
schedule → (2) run-now + run history UI → (3) failure alerts + (later) raw cron expressions.**

## Why

Today the only way to run a periodic command against an app is to bake it into the image (a cron
sidecar) or SSH into the box. Backups already prove the pattern works — exec a command in the
running service container, capture output, record a run, notify on failure. A scheduled job is
the same machine with the destination removed: instead of piping stdout to S3, we keep the tail
of it as a log and record success/failure. Nearly everything needed already exists.

## What already exists (don't rebuild)

- **The scheduler shape**: `backup/scheduler.go:49` — load enabled configs, compute each one's
  `nextOccurrence`, sleep to the soonest, run everything now due, loop. Idle cost is one sleeping
  goroutine. `nextOccurrence` (`scheduler.go:105`) already does daily/weekly with hour + day-of-week
  and is unit-tested (`scheduler_test.go`). Copy it verbatim.
- **The run lifecycle**: `backup/engine.go:66` `RunOnce` — open a `running` row, resolve the
  container, exec, record `success`/`failed`, notify on failure via the `Notifier` interface
  (`engine.go:30`). `job_runs` mirrors `backup_runs` one-for-one.
- **Container exec + output capture**: `dockercli.Compose.Exec` (`compose.go:199`) runs
  `docker exec <id> sh -c "<cmd>"`, streams stdout to an `io.Writer`, maps a non-zero exit to an
  error with stderr attached (`mapCmdError`). This is the whole execution primitive.
- **Container resolution incl. the not-running case**: `engine.go:130` `resolveContainer` —
  look up the service row, error if `container_id` is nil/empty ("service X has no running
  container"), else fall back to treating the name as a literal container. Reuse the logic.
- **Optional-subsystem gating**: the backup scheduler only starts when `ManagedServices` is on
  **and** at least one config exists (`main.go:346`), so it adds zero footprint otherwise. Jobs
  get the same treatment — but jobs are a *core* feature, not a managed-services add-on, so gate
  on "≥1 enabled job exists" alone (`CountScheduledJobs`), no master flag needed.
- **Notify plumbing**: `notify/events.go:13` event constants + `AllEvents`, per-event JSONB toggle,
  `Dispatcher.BackupFailed` (`dispatcher.go:289`) as the renderer template. Adding an alert = one
  `EventType` + one toggle key + a `JobFailed` dispatcher method + two ~10-line renderers.
- **Store + history pattern**: `store/backups.go` — `BackupConfig`/`BackupRun`, `CreateBackupRun`
  (opens `running`), `FinishBackupRun` (terminal + `finished_at`), `ListBackupRuns`,
  `CountFailedBackupRunsSince` (sidebar attention badge). `job_runs` copies all of it.
- **CRUD + run-now API + history routes**: `server.go:345-352` — `GET/POST /apps/{id}/backups`,
  `PUT/DELETE .../{cid}`, `GET .../{cid}/runs`, `POST .../{cid}/run`. The jobs routes are the same
  shape under `/apps/{id}/jobs`.
- **UI feature template**: `ui/src/features/backups/backups-page.tsx` + `lib/api/backups.ts` +
  `lib/backups.ts` (`scheduleSummary` formats daily/weekly into a label) + `types/api.ts`. A Jobs
  view is a near-copy with the destination column dropped and an output/exit column added.

## Key technical realities (read before building)

- **`vac-api` is off `vac-edge` and can't reach app containers directly — but it doesn't need to.**
  `docker exec` goes through the Docker socket, not the network, so jobs exec fine. This is the
  exact path backups already use; no Caddy/health-gate involvement.
- **There is no `docker compose run --rm` primitive in `dockercli`.** Only `Exec` into a running
  container exists (`compose.go`). A one-off-container model would mean building a new
  `compose run`/`docker run` wrapper, env-file plumbing, network attachment, and image-presence
  handling. **Recommendation: exec-into-the-running-service only, for v1** — it reuses the entire
  backup path and is what 90% of cron jobs want (the app's runtime + deps are already there).
  One-off containers are a real Phase-4 follow-up, not the starting point (see Out of scope).
- **If the service isn't running, the job fails fast, by design.** `resolveContainer` already
  returns "no running container"; record the run as `failed` with that message and notify. Do
  **not** try to start the stack to run a job — that crosses into deploy territory and can mask a
  down app. The history row tells the operator why.
- **No cron-expression dependency is present** (`grep` of `api/go.mod`: no `robfig/cron`). Pulling
  one in is ~1 small dep, but the binary targets <200 MB idle and the daily/weekly model already
  covers the overwhelming majority of operator cron needs. **Recommendation: ship the existing
  `nextOccurrence` (interval/daily/weekly + hour + day-of-week) first; add an optional raw-cron
  `schedule_expr` column later** if anyone needs "every 15 min" or "weekdays only". Be honest in
  the UI: v1 offers a frequency picker, not a cron textbox. A minute-level interval
  (`every_n_minutes`) is a cheap middle ground worth considering in Phase 1 since "warm the cache
  every 10 min" is common — the scheduler's soonest-sleep loop handles sub-hour waits fine.
- **Overlap guard is mandatory.** A 5-minute job that takes 8 minutes must not stack. Guard with a
  per-job in-flight set in the scheduler (skip-if-running, record nothing, log) **and** a hard
  per-run `timeout` (a `context.WithTimeout` around `Exec`, default e.g. 30 min, per-job
  configurable) so a hung command can't pin a container forever. Skip is the safer default over
  queueing for a single box.
- **Missed runs while VAC was down are not backfilled.** Like the backup scheduler, on boot we
  compute the *next* occurrence from now — a job whose slot passed during downtime simply runs at
  its next slot. Catch-up cron semantics are explicitly out of scope (one operator, not a batch
  system).
- **Timezone: `time.Local` (the host's TZ), same as backups + retention.** `nextOccurrence` uses
  `now.Location()`. Document it; don't add per-job TZ in v1.
- **Output capture is bounded.** Don't stream job stdout into `runtime_logs` (that's the
  log-follower's domain and would balloon the ring buffer). Capture into a capped buffer (e.g.
  last 16 KB) and store the tail on the `job_runs` row (`output` text) alongside the exit error —
  enough to debug, cheap to store, pruned with the run.

## Scope decisions (the important part)

1. **Exec-into-running-service, not one-off containers, for v1.** Reuses the backup path; covers
   the common case. One-off `docker run` is Phase 4.
2. **Frequency picker (interval/daily/weekly), not raw cron, for v1.** Reuse `nextOccurrence`; add
   `schedule_expr` later behind the same scheduler if demand appears.
3. **No master feature flag.** Jobs are core. The scheduler goroutine still only starts when
   ≥1 enabled job exists, so idle footprint stays zero — one `CountScheduledJobs` at boot.
4. **Skip-on-overlap + hard timeout.** No queue. Single box, predictable behavior.
5. **No missed-run backfill.** Next-slot semantics, matching backups.
6. **Bounded output tail on the run row**, not a stream into `runtime_logs`.
7. **New configs picked up on restart** (like the backup scheduler), with one improvement worth
   doing: the scheduler's `idle` re-check (`scheduler.go:31`, default 1h) means a newly-added job
   is picked up within the idle window without a restart — keep that, maybe shorten to ~5 min so
   "run now" isn't the only way to test a fresh job.

## Phase 1 — Data model + scheduler + execution (backend)

New package `api/internal/jobs/` (scheduler.go + engine.go), structured exactly like `backup/`.

Migration — two tables mirroring `backup_configs` / `backup_runs`:

```sql
scheduled_jobs(
  id, app_id (FK), name, service_name, command,
  frequency,            -- interval | daily | weekly   (v1)
  interval_minutes int, -- when frequency='interval'
  hour_of_day int, day_of_week int NULL,
  timeout_seconds int,  -- per-run hard cap
  enabled bool,
  last_run timestamptz NULL, next_run timestamptz NULL,  -- denormalized for the UI
  created_at, updated_at,
  UNIQUE(app_id, name)
)
job_runs(
  id, job_id (FK ON DELETE CASCADE),
  started_at, finished_at NULL,
  status,               -- running | success | failed | skipped | timeout
  exit_code int NULL, output text NULL, error text NULL
)
```

Store (`store/jobs.go`): `CreateScheduledJob`, `UpdateScheduledJob`, `GetScheduledJob`,
`ListScheduledJobsForApp`, `ListEnabledScheduledJobs` (scheduler working set),
`CountScheduledJobs`, `DeleteScheduledJob`; `CreateJobRun`/`FinishJobRun`/`ListJobRuns`/
`CountFailedJobRunsSince`. `last_run`/`next_run` updated as the scheduler runs.

`jobs.Scheduler` — copy `backup/scheduler.go` whole; extend `nextOccurrence` with an
`interval` branch (`now + interval_minutes`, anchored on `last_run` so it doesn't drift). Hold a
`map[jobID]struct{}` in-flight set for the overlap guard.

`jobs.Engine.RunOnce(ctx, job)` — copy `backup/engine.go` minus the destination: open a `running`
`job_runs` row, `resolveContainer`, `context.WithTimeout(timeout)`, `Exec` into a capped buffer,
record `success`/`failed`/`timeout` with `exit_code` + output tail + error, fire `JobFailed` on a
non-success. Update `scheduled_jobs.last_run`/`next_run`.

Wiring (`main.go`, next to the backup scheduler block ~`:344`): build `jobs.Engine` always (so
run-now works), start `jobs.Scheduler` when `CountScheduledJobs > 0`.

## Phase 2 — API + UI

API (`server/handler/jobs.go`, routes mirroring `server.go:345`):

```
GET    /api/apps/{id}/jobs            list configs
POST   /api/apps/{id}/jobs            create
PUT    /api/apps/{id}/jobs/{jid}      update
DELETE /api/apps/{id}/jobs/{jid}      delete
GET    /api/apps/{id}/jobs/{jid}/runs run history (incl. output tail)
POST   /api/apps/{id}/jobs/{jid}/run  run now (calls Engine.RunOnce in a goroutine)
```

UI — a **Jobs tab on app-detail** (lighter than a top-level page; jobs are app-scoped, like
backups-per-app before the fleet view). Near-copy of `features/backups/`:

- `lib/api/jobs.ts` + `useAppJobs(appId)` / `useRunJob` / CRUD mutations; `types/api.ts` add
  `ScheduledJob` + `JobRun`; `queryKeys` add a `jobs` key.
- A `jobs` feature folder: a service+command+schedule form (frequency picker, reusing the
  `scheduleSummary` formatter from `lib/backups.ts`, generalized), a runs table with status pill
  (reuse `StatusPill`), relative `last_run`/`next_run`, and an expandable output tail + exit code.
- A "Run now" button (reuse the backups `Play`/`toast` pattern) and an enable/disable toggle.

## Phase 3 — Failure alerts

`notify/events.go`: add `EventJobFailed` + to `AllEvents`; toggle key `job_failed`.
`dispatcher.go`: `JobFailed(appName, appID, jobName, errMsg)` copying `BackupFailed`
(`dispatcher.go:289`), plus the two ~10-line Discord/Slack renderers (Title "Job failed:
blog/cleanup", `OK:false`). `jobs.Engine.fail` calls it, exactly like backups. Optionally surface a
"N jobs failed" attention badge using `CountFailedJobRunsSince`, mirroring the backups badge.

## Out of scope (explicitly)

- **One-off / ephemeral containers** (`docker compose run --rm`, `docker run`) — no primitive
  exists; a real follow-up (Phase 4) once exec-jobs prove the model. Needs env/network/image
  plumbing the exec path gets for free.
- **Raw cron expressions** (`*/15 * * * *`) — frequency picker first; `schedule_expr` column +
  parser behind the same scheduler later, only if asked. No cron dep until then.
- **Missed-run backfill / catch-up** — next-slot semantics only.
- **Per-job timezone** — host `time.Local`, documented.
- **Streaming job output into the live log viewer** — bounded tail on the run row instead.
- **Fan-out / dependencies / job chaining** — single box, single command per job.

## Rough size

- Phase 1: 1 package (≈ two thin files copied from `backup/`), 1 migration (2 tables), ~10 store
  methods, the interval branch + overlap set + timeout. Medium — the scheduler/engine are mostly
  copy-adapt; the new bits are the in-flight guard and output capture.
- Phase 2: 1 handler (6 routes), 1 feature folder + api client + types. Medium (UI form + runs table).
- Phase 3: 1 event + 1 dispatcher method + 2 renderers + optional badge. Small.

## Build order

1. Migration + `store/jobs.go` (configs + runs, mirroring `store/backups.go`).
2. `jobs.Engine.RunOnce` (exec + timeout + capped output + record), tested with a fake `ExecRunner`.
3. `jobs.Scheduler` (copy `backup/scheduler.go`; add interval branch + overlap set), with
   `nextOccurrence` unit tests copied from `scheduler_test.go`.
4. Wire in `main.go` (engine always; scheduler gated on `CountScheduledJobs > 0`).
5. CRUD + run-now + runs API (`handler/jobs.go`, routes in `server.go`).
6. UI Jobs tab: api client, types, feature folder, form + runs table + run-now.
7. `EventJobFailed` + `JobFailed` dispatcher + renderers + optional badge.
8. `/code-review` + `/simplify`; `/refresh-kb` (new `jobs` package → `architecture.md`; new
   endpoints → re-verify the route map).

## Verification

- A daily job at hour H execs in the running service container at H (host TZ) and records a
  `success` run with the output tail; a non-zero exit records `failed` with stderr + exit code.
- A job whose service is stopped records `failed` "no running container", fires `EventJobFailed`,
  and does **not** start the stack.
- An interval job whose command outruns its interval is **skipped**, not stacked; a job exceeding
  its `timeout_seconds` records `timeout` and releases the container.
- Deleting an app cascades its jobs + runs; deleting a job cascades its runs.
- Boot with zero jobs starts no scheduler goroutine (idle footprint unchanged); adding the first
  job and restarting (or waiting one idle cycle) starts it.
- `make lint typecheck test` clean.
