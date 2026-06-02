# Track B — Observability & Limits — execution plan

> Working plan for executing **Track B** of [`00-parallel-tracks.md`](00-parallel-tracks.md)
> in this worktree, to be merged later. Track A (Deploy Core) runs concurrently in a separate
> worktree — this plan is arranged to stay out of `api/internal/deploy`, `caddy`, and `proxy`
> as much as possible and to claim a non-colliding migration range.
>
> Sequence: **B1 `07` RAM benchmark → B2 `13` Prometheus → B3 `06` Resource guardrails.**
> Owns: `stats`, `reqmetrics`, host stats, `config`/build (`GOMEMLIMIT`, Makefile, CI),
> compose generation, dashboard meters UI.

## Pre-flight: locked decisions (resolve the stubs' open questions before coding)

These are decided up front so nothing churns mid-track.

1. **Migration range — reserve `00030`–`00039` for Track B.** Stage 0 used `00019`/`00020`;
   Track A will take the next contiguous numbers for rollback/push. Track B only needs
   migrations in B3 (per-app RAM limit; OOM events) — placing them at `00030+` leaves a gap
   so neither track rebases the other's numbers at merge. Coordinate if Track A approaches
   `00030`.
2. **`/metrics` auth — token-gated bearer, not session.** A scraper can't carry a session
   cookie + CSRF. Add a dedicated `VAC_METRICS_TOKEN` (env-only secret, like `CaddyAskToken`);
   `/metrics` is served outside the `/api` session group and checked against a constant-time
   compare. If the token is unset, `/metrics` returns `404` (feature off) rather than open —
   it leaks topology, so default-closed. (Reuse the existing API-token table later if desired,
   but a single scrape token is the smallest correct thing now.)
3. **Metric naming — lock the namespace `vac_` now** (renames break user scrapers and plan 12
   dashboards). Names + labels frozen in the B2 section below.
4. **OOM detection — `die` event + `docker inspect` confirm.** The event stream does **not**
   carry `OOMKilled`; on a `die` event with exit code `137` we confirm via
   `docker inspect → State.OOMKilled` before labelling, to avoid mislabeling ordinary
   SIGKILLs. (Verified: `dockerevents.Bus` surfaces `die` with `Actor.Attributes["exitCode"]`;
   no OOM flag in the event.)
5. **Box budget — soft warn, never hard block.** Over-allocation shows a warning meter; we do
   not refuse a deploy. Hard-blocking a single-operator box is hostile and risks wedging a
   legitimate change. (Revisit only if users ask.)
6. **No new collectors.** B2 is pure read-model exposition over existing sources
   (`stats.HostCollector`, `stats.Manager`, `reqmetrics` store, `deployments` table). B1 reads
   cgroup + `runtime.ReadMemStats`. Only B3 adds data (RAM limit column, OOM events).

---

## B1 — `07` Idle-RAM benchmark harness  *(effort S–M)*

**Goal:** make "control plane idles < 200 MB RAM (excl. DB)" a repeatable, CI-enforced number.
Do this first — it guards the headline claim before B2/B3 add weight.

### Deliverables

1. **In-process mem introspection.** Register an `expvar` handler at `/debug/vars` in
   `api/internal/server/server.go`, guarded by the same `VAC_METRICS_TOKEN` as `/metrics`
   (default-closed). `expvar` already publishes `memstats` (`runtime.ReadMemStats`) — exposes
   `HeapAlloc` / `HeapSys` / `Sys` so the bench can separate Go heap from total RSS. Add a tiny
   `GET /debug/gc` (token-gated) that calls `runtime.GC()` + `debug.FreeOSMemory()` so the
   bench can force a steady state before measuring.
2. **Runtime memory ceiling.** Set `GOMEMLIMIT` (~`180MiB` soft) and `GOGC` in
   **`compose.prod.yaml`** + dev `compose.yaml`, and a hard `mem_limit: 256m` on the `vac-api`
   service. A regression then OOMs in testing, not on a user's box.
   - **As-built deviation:** GOMEMLIMIT/GOGC are honoured *natively* by the Go runtime from the
     environment — no `config` fields or `debug.SetMemoryLimit` re-parsing of byte-size strings
     (which would just duplicate the runtime's own parser, a footgun). `main()` logs the active
     soft limit at boot via `debug.SetMemoryLimit(-1)` so it's observable. Only `MetricsToken`
     was added to `config` (`VAC_METRICS_TOKEN`).
   - **As-built deviation:** used compose `mem_limit` rather than `deploy.resources.limits.memory`
     — `mem_limit` is the field reliably enforced by `docker compose up` (non-swarm), which is
     how VAC runs. Both are overridable: `VAC_GOMEMLIMIT`, `VAC_GOGC`, `VAC_API_MEM_LIMIT`.
3. **`make bench-ram` target + script** (`scripts/bench-ram.sh`):
   boot fresh stack → deploy 3–4 tiny apps (exercise log followers, stats, reqmetrics, ws hub)
   → idle 60–120 s → `curl /debug/gc` → read `docker stats --no-stream vac-api` (cgroup v2
   `memory.current`) → cross-check `/debug/vars` `Sys`/`HeapAlloc` → print breakdown →
   assert `< 200 MB`, **warn at 180 MB**, hard-fail (exit 1) at 200 MB.
4. **CI job.** Add `.github/workflows/ci.yml` (none exists today — only release/installer
   workflows). Jobs: `lint` (`make lint`), `test` (`make test`), `bench-ram` (needs Docker;
   `runs-on: ubuntu-latest`, fixed size). `bench-ram` warns at 180/fails at 200.
   - Side win: this is the first real test/lint CI for the repo — keep it minimal and fast.

### Cheap wins to check while here (from the stub)

- Tune `GOGC`/`GOMEMLIMIT` empirically against the bench number.
- Audit ring buffers: `cfg.LogRingBuffer` (10k lines/service) × many services. Confirm the
  reqmetrics tailer and ws hub buffers aren't over-sized for a 2 GB box.

### Acceptance

`make bench-ram` prints a steady-state number + heap/RSS breakdown and exits non-zero above
200 MB; CI runs it on every push.

### Files touched

`api/internal/config/config.go` (GoMemLimit/GoGC), `api/main.go` (apply limits),
`api/internal/server/server.go` (`/debug/vars`, `/debug/gc`, token guard),
`compose.prod.yaml` (mem limit + env), `Makefile` (`bench-ram`), `scripts/bench-ram.sh` (new),
`.github/workflows/ci.yml` (new). **No `deploy`/`caddy`/`proxy` edits → no Track A collision.**

---

## B2 — `13` Prometheus metrics exposition  *(effort S–M)*

**Goal:** expose VAC's own metrics in Prometheus text format on `vac-api`. Standalone-useful;
unblocks plan 12. Pure read-model — reuses existing sources.

### Endpoint & auth

- `GET /metrics` registered **outside** the `/api` session group in `server.go`, wrapped by a
  small `RequireMetricsToken` middleware (constant-time compare of `Authorization: Bearer …`
  against `VAC_METRICS_TOKEN`). Unset token → `404`.
- Hand-write the exposition (no `prometheus/client_golang` dependency — keeps the RAM budget
  and binary small; we already manually parse Caddy's `/metrics` in `reqmetrics/scraper.go`,
  so the project precedent is no-client-lib). A ~100-line `internal/promexport` package that
  formats `# HELP`/`# TYPE` + samples from snapshots.

### Locked metric set (names + labels frozen)

| Metric | Type | Labels | Source |
|---|---|---|---|
| `vac_host_cpu_percent` | gauge | — | `stats.HostCollector.Snapshot` |
| `vac_host_mem_used_bytes` / `vac_host_mem_total_bytes` | gauge | — | host snapshot |
| `vac_host_disk_used_bytes` / `vac_host_disk_total_bytes` | gauge | — | host snapshot |
| `vac_host_request_rate` | gauge | — | host snapshot (Caddy delta) |
| `vac_app_cpu_percent` | gauge | `app`, `service` | `stats.Manager` one-shot collect |
| `vac_app_mem_bytes` | gauge | `app`, `service` | per-service sample |
| `vac_requests_total` | counter | `app`, `service` | `reqmetrics` store (sum of buckets) |
| `vac_request_errors_total` | counter | `app`, `service` | reqmetrics (5xx) |
| `vac_deploys_total` | counter | `app`, `status`, `triggered_by` | `deployments` table |
| `vac_deploy_duration_seconds` | gauge (last) | `app` | `finished_at − started_at` |
| `vac_build_info` | gauge=1 | `version`, `commit` | ldflags vars |

- **Scrape-time collection.** `/metrics` calls `HostCollector.Snapshot(ctx)` and a new
  `stats.Manager.SnapshotAll(ctx)` (one-shot `docker stats --no-stream` across running
  services — does **not** touch the subscriber-gated live collectors). reqmetrics + deploy
  metrics are SQL aggregates added to the store:
  - `store.CountDeploymentsByStatus(ctx) []{App,Status,TriggeredBy,Count}`
  - `store.SumRequestMetrics(ctx) []{App,Service,Requests,Errors}` (over the retained window)
- Counters are derived from current DB totals; that's fine for Prometheus (monotonic within a
  process lifetime; documented caveat: resets if the retention window drops old rows — acceptable
  for `requests_total` since it's a rolling window, note it in the metric `# HELP`).

### Open-question resolutions

- **Auth:** token-gated (decision #2). **Naming:** frozen above. **History:** none in VAC —
  leave history to whoever scrapes (matches deviation D6, stats are live-only).

### Acceptance

`GET /metrics` with the bearer token returns valid Prometheus exposition covering host +
per-app + deploy + request metrics; `promtool check metrics` passes; external Prometheus
scrapes with stable names. Unset token → 404; wrong token → 401.

### Files touched

`api/internal/promexport/*` (new), `api/internal/server/server.go` (route + middleware),
`api/internal/server/middleware/metrics_token.go` (new), `api/internal/stats/manager.go`
(`SnapshotAll`), `api/internal/store/{deployments,request_metrics}.go` (aggregate queries),
`api/internal/config/config.go` (`MetricsToken`). **No `deploy`/`caddy`/`proxy` edits.**

---

## B3 — `06` Resource guardrails  *(effort M)*

**Goal:** make the box hard to accidentally OOM — per-app RAM limit, box budget UI, OOM
detection + labelling + notify.

### Schema (migration `00030`)

- `ALTER TABLE apps ADD COLUMN mem_limit_mb INT;` (NULL = unlimited / use default).
  *Alternative considered:* stash in the opaque `build_config` JSON — rejected; RAM limit is a
  first-class runtime knob the box-budget aggregate must `SUM()` in SQL, so it earns a column.
- `ALTER TABLE services ADD COLUMN oom_killed_count INT NOT NULL DEFAULT 0;`
- Optional `oom_events` table only if we want a history feed; **start without it** — surface the
  latest OOM via service status + a notification + runtime-log line (cheaper, matches crash-loop
  precedent). Revisit if the UI wants a timeline.

### Backend

1. **Enforce the limit.** Thread `App.MemLimitMB` into compose generation. `compose/wrap.go`
   templates (`generatedTemplate`, `dockerfileTemplate`) gain a
   `deploy.resources.limits.memory` block when a limit is set. For user-provided composes we
   **do not rewrite their file**; instead pass the limit via a generated override
   (`-f compose.yaml -f vac-override.yaml`) at `docker compose up` — keeps user files
   untouched. *(This is the one spot that brushes the deploy pipeline; coordinate with Track A
   — it's additive (an extra `-f` override file), not a rewrite of the up/health/route logic.)*
2. **OOM detection.** Extend `crashloop.Monitor` (it already consumes `die` events with exit
   codes): on `die` with code `137`, `docker inspect` the container; if `State.OOMKilled`,
   increment `services.oom_killed_count`, set a distinct status label, write a system runtime
   log ("out of memory — killed; 512 MB limit"), and fire a notification.
3. **Notify.** Add `notify.EventType "oom_killed"` to `events.go` + `AllEvents`, and a
   `Dispatcher.OOMKilled(appName, appID, service string, limitMB int)` entry point following
   the existing `CrashLoop` shape. Wire `crashloop.Monitor`'s notifier to call it.
4. **Box budget read-model.** `GET /api/host/budget` → `store.SumAppMemLimits()` +
   `HostCollector.Snapshot` total RAM: `{ total_ram_mb, allocated_mb, app_count, over_committed }`.

### UI

- **App → Settings → new "Runtime" section** (between Build and Danger Zone in
  `settings-tab.tsx`): numeric "RAM limit (MB)" input → `useUpdateApp({ mem_limit_mb })`.
  Extend `App` / `UpdateAppInput` types with `mem_limit_mb?: number`.
- **Box budget panel** on the dashboard: the existing "Container budget" card in
  `apps-dashboard.tsx` already renders `Meter` rows — add an "allocated vs total RAM" meter fed
  by a new `useBoxBudget()` query (`/api/host/budget`, 5 s refetch). Red fill + "over-allocated"
  warning when `allocated > total`.
- **OOM labelling.** Surface `oom_killed_count` / OOM status on the service card next to the
  restart count + last-exit-code (reuse `StatusPill`).

### Acceptance

Setting a RAM limit enforces it on the container (verify `docker inspect` memory limit);
dashboard shows allocated-vs-total with an over-commit warning; an OOM kill is labelled as such
(distinct from crash-loop) and a notification fires.

### Files touched

`api/internal/db/migrations/00030_resource_guardrails.sql` (new),
`api/internal/store/{apps,services}.go`, `api/internal/compose/wrap.go` + override generation,
`api/internal/crashloop/monitor.go` (OOM confirm), `api/internal/notify/{events,dispatcher}.go`,
`api/internal/server/handler/{apps,host_budget}.go`, plus UI: `settings-tab.tsx`,
`apps-dashboard.tsx`, `lib/api/{apps,metrics}.ts`, `types/api.ts`, service card.
**Compose override is the only deploy-adjacent touch → flag at merge with Track A.**

---

## As-built status (this worktree)

All three phases implemented and tested in this worktree (`pale-pike`), to be merged later.

- **B1** ✅ — `VAC_METRICS_TOKEN` config; token-gated `/debug/vars` + `/debug/gc`; `GOMEMLIMIT`/
  `GOGC` env + `mem_limit: 256m` in `compose.prod.yaml`; `make bench-ram` + `scripts/bench-ram.sh`;
  first-ever `.github/workflows/ci.yml` (lint/test/bench-ram). Unit-tested. **Not** verified
  end-to-end locally — the full `make bench-ram` Docker build filled the dev host disk; run it
  on the CI runner. Bench covers *baseline idle* (no user apps); per-app-collector load is a
  follow-up (noted in the script header).
- **B2** ✅ — `internal/promexport` (dependency-free formatter), `/metrics` token-gated route,
  `stats.Manager.SnapshotAll`, store aggregates (`CountDeploymentsByStatus`,
  `LatestDeployDurations`, `SumRequestMetrics`, `ListRunningServices`). Unit-tested
  (format + escaping + degrade-on-error). `promtool` not available locally.
- **B3** ✅ — migration `00030` (`apps.mem_limit_mb`, `services.oom_killed_count`); RAM-limit
  enforcement via compose override (`compose.WriteResourceOverride` + variadic `Up`, one
  flagged deploy-pipeline line); OOM detection in `crashloop.Monitor` (die→inspect, notify once
  per episode, count every kill); `notify` `oom_killed` event; `GET /api/host/budget`; UI
  (Settings → Runtime RAM input, dashboard Allocated-RAM meter + over-commit warning, service-card
  OOM label). Unit-tested. DB-dependent paths (migration, store columns) verified by compile +
  unit tests; full integration needs the Docker test stack (run in CI).

### Pre-existing issues surfaced by the new CI (NOT introduced by Track B)

The new `ci.yml` runs lint/test/typecheck, which the repo never had before — so it surfaces
two latent issues already present on this branch (confirmed by stashing all Track-B changes):

1. **`ui/src/routes/_app.tsx` had a committed debugging hack — now removed.** Lines 11–13 were
   an early `return` marked *"TEMP (local UI preview, do not commit): skip auth/setup guard so
   the dashboard renders without a backend."* It disabled the entire auth/setup guard and made
   the rest of the function unreachable (the source of the `'err' is of type 'unknown'`
   typecheck error). It was introduced in `26d21b1` and slipped into a large UI-polish commit.
   **Removed before the merge to `main`** — `beforeLoad` now runs the real setup/session guard
   again.
2. (Fixed) `status-filter.test.ts` App fixture was missing required fields — updated for the new
   `mem_limit_mb` field while there.

Also fixed in passing: a latent `-race` flake in `internal/logstream`'s
`TestFollowerCapturesAndPublishes` (asserted the publish count without waiting for it) — the new
CI runs `-race`, so it had to be deflaked. Test-only change.

## Cross-track sync points (from `00`)

- **Migrations:** Track B owns `00030`–`00039` (only `00030` used). Watch Track A's numbering.
- **`13` ↔ `12`:** plan 12 (other track/later) consumes `/metrics`; the frozen names above are
  the contract. Don't rename.
- **`06` compose override ↔ Track A pipeline:** additive extra `-f` file, not a pipeline
  rewrite — but call it out explicitly in the merge PR so A's owner sanity-checks the up step.

## Suggested commits (Conventional Commit, commitlint-compatible)

- `feat(bench): idle-RAM harness, GOMEMLIMIT, and CI guard for the <200MB claim` (B1)
- `feat(metrics): token-gated Prometheus /metrics exposition` (B2)
- `feat(limits): per-app RAM limits, box budget, and OOM detection` (B3)

Run `/code-review` + `/simplify` after each, and `/refresh-kb` at the end (touches
`architecture.md` — new `promexport` package + `/metrics`/`/debug` seams).
