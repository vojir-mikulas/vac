<!-- generated from commit 0f94e36 on 2026-05-31 — regenerate with /refresh-kb; if HEAD has moved past this commit and api/internal/{deploy,compose,dockercli,proxy,caddy}/ changed, treat as possibly stale -->

# Deployment flow — git → build → run → route

The end-to-end path a deploy takes. Symbol names are given at package/function level
(line numbers are intentionally omitted — they drift; grep the function name). The status a
deployment shows at each step is in **bold**.

## 0. Trigger → queue

- `POST /api/apps/{id}/deployments` → handler `TriggerDeployment` in
  `server/handler/deployments.go`. It validates the app, inserts a deployment row
  (**queued**), and calls `deploy.Worker.Enqueue(deploymentID)`. Returns `202` immediately, or
  `503` if the queue is full (`deploy.ErrQueueFull`).
- `deploy/worker.go` — a single goroutine drains a bounded channel (default cap 32) and runs
  the pipeline one deploy at a time. On boot it sweeps any non-terminal deployments from a
  previous process to **interrupted** (`store.MarkInProgressDeploymentsInterrupted`).

## 1. Clone / pull (**cloning**)

- `deploy/pipeline.go` materializes the app's SSH deploy key (decrypted via `sshkey.Manager`,
  written to a temp file, referenced through `GIT_SSH_COMMAND`, cleaned up after).
- `gitcli/gitcli.go`: `LsRemote` (cheap pre-check) → `Clone` (shallow `--depth=1
  --single-branch`) or, if the repo dir already exists, `Pull` (fetch + hard reset to
  `origin/{branch}`). `HeadCommit` extracts the short SHA + message, stored on the deployment
  row.

## 2. Detect build type

- `compose/detect.go` `Detect` looks, in order, for `compose.yaml` → `docker-compose.yml` →
  `Dockerfile`. No match ⇒ `ErrNoComposeOrDockerfile`.
- If only a `Dockerfile` is found, `compose/wrap.go` `Wrap` writes a minimal generated
  `compose.yaml` (single `app` service, `build: .`, `restart: always`, `env_file: .env`). The
  generated file is untracked and regenerated each deploy.

## 3. Build (**building**)

- `dockercli/compose.go` `Compose.Build` runs
  `docker compose -p vac-{slug} -f {composeFile} build` with BuildKit enabled. Project name is
  always `vac-{slug}` so VAC stacks never collide with manually-managed compose projects on the
  host.
- Build output is streamed line-by-line through the deploy log writer (`deploy/loggers.go`):
  persisted to `deployment_logs` and published live to the WS topic `build:{deploymentID}`.

## 4. Up (**deploying**)

- Env vars are decrypted and rendered to an env file, then `Compose.Up` runs
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
  (`deploy/status.go` `DeriveAppStatus`).
- Health failure ⇒ deployment **error** but the stack is **not** torn down; app goes
  **degraded** and keeps serving the prior version.
- Build/clone errors ⇒ deployment **error**, app **error**, `notify.DeployFailed` fires. The
  previously running stack is left untouched.

Terminal deployment statuses: `running`, `error`, `interrupted`.

## After deploy — the always-on subsystems

These run independently of any single deploy, fed by the shared `dockerevents` bus:

- **Runtime logs** (`logstream/`): a follower per live container tails `docker logs --follow`
  into a ring buffer in `runtime_logs` and publishes to a per-app logs topic. Always on (logs
  must persist for the Logs Explorer and crash-loop forensics). Reconciled after each deploy.
- **Stats** (`stats/`): per-app `docker stats` collector runs **only while a WS subscriber is
  attached** (live-only, never persisted); host stats via gopsutil on the `host` topic.
- **Crash-loop** (`crashloop/`): counts `die` events in a sliding window (default ~5 restarts /
  ~2 min); on trip it stops the service, marks it `crash-loop`, and fires `notify.CrashLoop`.

## Status vocabulary

- Deployment: `queued → cloning → building → deploying → health-checking → running` (or
  `error` / `interrupted`). Defined in `deploy/status.go`.
- Service: `running / deploying / degraded / crash-loop / stopped / error`, mapped from Docker
  state by `MapPsStateToServiceStatus`.
- App: collapsed from its services by `DeriveAppStatus` (crash-loop > building > deploying >
  error > degraded > running/created).
