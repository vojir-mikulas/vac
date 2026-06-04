<!-- generated from commit def192a on 2026-06-04 — regenerate with /refresh-kb; if HEAD has moved past this commit and api/internal/{deploy,adapter,compose,dockercli,proxy,caddy}/ changed, treat as possibly stale -->

# Deployment flow — git → build → run → route

The end-to-end path a deploy takes. Symbol names are given at package/function level
(line numbers are intentionally omitted — they drift; grep the function name). The status a
deployment shows at each step is in **bold**.

## 0. Trigger → queue

- `POST /api/apps/{id}/deployments` → handler `TriggerDeployment` in
  `server/handler/deployments.go`. It validates the app, inserts a deployment row
  (**queued**), and calls `deploy.Worker.Enqueue(deploymentID)`. Returns `202` immediately,
  `503` if the queue is full (`deploy.ErrQueueFull`), or `409` if the app already has a
  non-terminal deploy (manual/rollback) — the webhook path coalesces to `202` instead.
- **Webhook trigger.** Inbound Git webhooks land on the `webhook` handler/package, which
  authenticates against the per-app secret (`apps.webhook_secret_enc`) and matches `ParseRef`
  against the app's `deploy_triggers` rows to decide whether (and on which branch) to deploy.
- **Per-app guard.** A partial unique index `one_active_deploy_per_app` (migration 00062)
  allows at most one non-terminal deployment per app. `store.CreateDeployment` /
  `CreateRollbackDeployment` translate the unique violation to `ErrActiveDeploymentExists`.
  This makes coalescing atomic across every trigger path (closing the old check-then-insert
  race) and guarantees two pool workers never pick up two deploys for the same app.
- `deploy/worker.go` — a pool of N goroutines (N = `max_concurrent_deploys` instance setting,
  default 1, clamped to 1..`deploy.MaxConcurrency`=8, sized at boot) drains a bounded channel
  (default cap 32). N>1 only ever runs deploys for *different* apps concurrently (the per-app
  guard). On boot it sweeps any non-terminal deployments from a previous process to
  **interrupted** (`store.MarkInProgressDeploymentsInterrupted`).
- **Reaper.** A periodic goroutine (`startReaper`, ~1 min) settles deployments stuck
  non-terminal past a timeout (~30 min) to **error** — backstop against a hung subprocess.
- **Cancellation.** Each in-flight deploy runs under a per-deploy context registered in the
  worker. `POST .../deployments/{did}/cancel` → `CancelDeployment` aborts the running
  subprocess (`Worker.Cancel`) and settles the row **canceled** (a terminal status distinct
  from `interrupted`); the app status is recomputed from current service statuses. A
  still-queued deploy is settled directly and skipped when dequeued. The running stack is
  never torn down.
- **Live queue.** Producers publish a payload-less change frame to the `deployments` WS topic
  on every create/transition/settle; `GET /api/deployments/active` is the snapshot and
  `GET /api/deployments/stream` pushes a fresh snapshot per change (`store.ListActiveDeployments`).

## 1. Acquire source (**cloning**)

- `deploy/pipeline.go` materializes the app's SSH deploy key (decrypted via `sshkey.Manager`,
  written to a temp file, referenced through `GIT_SSH_COMMAND`, cleaned up after).
- `gitcli/gitcli.go`: `LsRemote` (cheap pre-check) → `Clone` (shallow `--depth=1
  --single-branch`) or, if the repo dir already exists, `Pull` (fetch + hard reset to
  `origin/{branch}`). `HeadCommit` extracts the short SHA + message, stored on the deployment
  row. **Rollbacks** pin to a prior commit via `FetchCommit` (fetch that SHA, or deepen the
  shallow clone, then detached checkout).
- **Template-sourced apps** (add-ons / managed DBs) skip git entirely: the pipeline calls
  `Templates.Materialize()` to write embedded files into the work dir.

## 2. Resolve build → compose file

- An **adapter** layer normalizes the build source to a single compose file: `adapter.For`
  selects the right adapter (compose / Dockerfile / framework / static) and `Prepare`
  generates or returns the compose file.
- `compose/detect.go` `Detect` looks, in order, for `compose.yaml` → `compose.yml` →
  `docker-compose.yml` → `docker-compose.yaml` → `Dockerfile`. No match ⇒
  `ErrNoComposeOrDockerfile`.
- If only a `Dockerfile` is found, `compose/wrap.go` `Wrap` writes a minimal generated
  `compose.yaml` (single `app` service, `build: .`, `restart: always`, `env_file: .env`). The
  generated file is untracked and regenerated each deploy.
- **Preflight lint** runs before the build on the *resolved* compose: the pipeline first runs
  `dockercli.Compose.Config` (`docker compose -p vac-{slug} config`) and lints the rendered
  output via `compose.PreflightBytes`, falling back to `compose.Preflight` on the raw file if
  `config` can't render. VAC-incompatible constructs (host-escape, edge-network conflicts) are
  hard findings that block the deploy (→ degraded); others log as warnings. An allow-unsafe
  override exists but never downgrades host-escape.
- A SHA256 of the resolved compose file is stored on the deployment row for change detection.

## 3. Build (**building**)

- `dockercli/compose.go` `Compose.Build` runs
  `docker compose -p vac-{slug} -f {composeFile} build` with BuildKit enabled. Project name is
  always `vac-{slug}` so VAC stacks never collide with manually-managed compose projects on the
  host.
- Build output is streamed line-by-line through the deploy log writer (`deploy/loggers.go`
  `LogWriter`): persisted to `deployment_logs` and published live to the WS topic
  `build:{deploymentID}`.

## 4. Up (**deploying**)

- Env vars are decrypted and rendered to an env file (when a `crypto.Box` is available), and a
  per-app RAM cap is layered on via `compose.WriteResourceOverride` (an extra `-f` file, never
  rewriting the user's compose). Then `Compose.Up` runs
  `docker compose -p vac-{slug} --env-file … up -d --remove-orphans`.
- `Compose.Ps` lists the resulting containers; the pipeline upserts a `services` row per
  service (`store.UpsertService`) recording container id, internal/published ports, and a
  status mapped from the Docker state.

## 5. Attach + route (**health-checking**)

- `vac-edge` network is ensured (`dockercli/network.go`, idempotent).
- For each HTTP-exposing service the `proxy` package attaches the container to `vac-edge` with
  alias `{slug}--{service}` (`NetworkConnect`) and pushes a Caddy route via the `caddy` admin
  client (`PutRoute`): host-match on the domain, reverse-proxy upstream
  `{slug}--{service}:{internal_port}`, with Caddy **active health checks** configured.
- Auto-domains (`{slug}.{VAC_BASE_DOMAIN}`, or `{service}.{slug}.{base}` for multi-service apps)
  are **derived at reconcile** from the app's HTTP services + base domain — not stored rows (plan
  09 F1). They emit `vac-auto-{appID}-{service}` routes alongside custom-domain `vac-route-{id}`
  routes; stale routes of both kinds are pruned, so a base-domain change leaves no orphans.

## 6. Health gate → terminal

- The pipeline polls Caddy's `/reverse_proxy/upstreams` admin endpoint to confirm the upstream
  is healthy before declaring success. **This is why `vac-api` being off `vac-edge` matters** —
  it cannot probe the container directly, so Caddy is the health authority.
- Success ⇒ deployment **running**, app status derived from service statuses
  (`deploy/status.go` `DeriveAppStatus`). The `Reconciler` hook (logstream supervisor) then
  attaches log followers to the fresh containers, and the optional `notify` hook fires.
- Health failure ⇒ deployment **error** but the stack is **not** torn down; HTTP-exposing
  services go **degraded** (portless services untouched) and the app keeps serving the prior
  version.
- Build/clone errors ⇒ deployment **error**, app **error**, `notify.DeployFailed` fires. The
  previously running stack is left untouched.

## After deploy — the always-on subsystems

These run independently of any single deploy, fed by the shared `dockerevents` bus:

- **Runtime logs** (`logstream/`): a follower per live container tails `docker logs --follow`
  into a ring buffer in `runtime_logs` and publishes to a per-app logs topic. Always on (logs
  must persist for the Logs Explorer and crash-loop forensics). Reconciled after each deploy.
- **Stats** (`stats/`): per-app `docker stats` collector runs **only while a WS subscriber is
  attached** (live-only, never persisted); host stats via gopsutil on the `host` topic.
- **Crash-loop** (`crashloop/`): counts `die` events in a sliding window (default ~5 restarts /
  ~2 min); on trip it stops the service, marks it `crash-loop`, and fires `notify.CrashLoop`.
- **Cert / domain health** (`certcheck`, `certprobe`, `domainstatus`): periodically observe TLS
  leaf-cert expiry and DNS/cert health for managed hosts; expiry alerts go through `notify`.

## Status vocabulary

- Deployment: `queued → cloning → building → deploying → health-checking → running` (or
  terminal `error` / `interrupted` / `canceled`). Defined in `deploy/status.go`.
- Service: `running / deploying / degraded / crash-loop / stopped / error`, mapped from Docker
  state by `MapPsStateToServiceStatus`.
- App: collapsed from its services by `DeriveAppStatus` (crash-loop > building > deploying >
  error > degraded > running/created).
