# Phase 2 — Deployment Pipeline

## Goal

A `vac-api` binary that can take an app record from Phase 1 (name, git URL, branch) and turn it
into a running Docker Compose stack on the host. By the end of Phase 2 you can:

1. Click "Test connection" on a new app and see whether VAC's SSH key reaches the repo.
2. Click "Deploy" — VAC clones the repo, detects (or auto-generates) a compose file, runs
   `docker compose build` + `docker compose up`, and persists the service list plus per-step
   build logs.
3. Watch the deployment progress through the `building → deploying → running` state machine
   in the API (no live streaming UI yet — that's Phase 4; logs land in Postgres and can be
   polled).
4. See a service get stopped automatically when it crash-loops past the threshold.
5. Trust that old build/runtime log rows are pruned on schedule so the DB doesn't grow forever.

No reverse proxy, no TLS, no real-time WebSocket streaming, no UI beyond placeholder pages —
those are Phases 3, 4, and 5. Phase 2 produces a deployment **engine** with a REST surface.

Reference: see `mvp.md` § Build Phases → Phase 2 and § Deployment Pipeline for original scope.
This document sequences that scope, picks libraries, and defines exit criteria.

---

## Scope

### In

- Per-app ED25519 SSH key pair generation (private key encrypted at rest)
- `GET/POST/DELETE /api/apps/:id/ssh-key` — fetch public key, regenerate, delete
- `POST /api/apps/:id/test-connection` — wraps `git ls-remote`, returns categorised result
- Git clone (first deploy) + pull (subsequent deploys), SSH and HTTPS
- Compose file detection: `compose.yaml` → `docker-compose.yml` → auto-generated wrapper
  for Dockerfile-only repos
- `.dockerignore` presence check — warning logged to the build log when missing
- `docker compose build` with `DOCKER_BUILDKIT=1`, line-by-line log capture into Postgres
- `.env` file synthesis from encrypted `EnvVars` rows (Phase 1 deferred this — see M3 below)
- `docker compose up -d --remove-orphans` with project name `vac-{slug}`
- Service detection: parse `docker compose ps --format json`, upsert `services` rows
- Richer status model: `building / deploying / running / degraded / crash-loop / stopped /
  error / interrupted`
- Basic per-service health check: HTTP `GET /` on the exposed port, configurable retries
- Crash-loop monitor: Docker event subscription per app, count restarts in window, stop
  the service and mark `crash-loop` when threshold crossed
- Deployment history: list, get-by-id, get build logs by deployment
- Stack control endpoints: `POST /api/apps/:id/start | stop | restart`,
  `POST /api/apps/:id/services/:name/restart`
- Env var management endpoints: `GET /api/apps/:id/env`, `PUT /api/apps/:id/env`
- Service config endpoints: `GET /api/apps/:id/services`,
  `PATCH /api/apps/:id/services/:name` (set `exposed_port` for health check; domain field
  exists but Caddy wiring is Phase 3)
- Log retention pruner — nightly goroutine deletes runtime logs older than
  `VAC_LOG_RETENTION_DAYS`, activity events older than `VAC_ACTIVITY_RETENTION_DAYS`
- Image pruning after successful deploy — keep last `VAC_IMAGE_KEEP_COUNT` images per service
- Graceful interrupt: a deployment running when `SIGTERM` arrives is marked `interrupted`
  (Phase 1 already shuts down cleanly; Phase 2 adds the marker)
- Integration tests against a real Docker daemon for the clone → build → up happy path

### Out (deferred to later phases)

- Caddy integration, custom domains, automatic subdomains, TLS (Phase 3)
- WebSocket hub, live build log streaming, live runtime logs, stats streaming (Phase 4)
- Notifications (Discord / Slack) for deploy success/fail and crash-loop events (Phase 4)
- Dashboard UI for any of the above (Phase 5)
- Caddy metrics scraping → request-rate stats (Phase 3)
- `.env` paste import UI helper (Phase 5; the API endpoint accepts arbitrary JSON already)
- Auto-deploy on git webhook (post-MVP)

---

## Key technical decisions

### Shell out to `git` and `docker compose` rather than use SDKs

Phase 2 wraps two CLIs instead of using `go-git` or the Docker Engine SDK directly:

- **Git CLI**: `go-git` is missing battle-tested SSH host-key handling, struggles with
  submodules, and lags behind `git` on protocol edge cases. The user will run any Git host
  (GitHub, GitLab, Gitea, Bitbucket, self-hosted), so robustness wins.
- **Docker CLI + Compose v2 plugin**: the Compose v2 API surface is large and only the CLI
  is officially stable. We do still use the Docker Engine SDK
  (`github.com/docker/docker/client`) for fine-grained operations: `docker events` streaming
  (crash-loop monitor), `docker inspect` (status reads), and image pruning. Compose is for
  build + up + down; Engine SDK is for observation.

Both CLIs are invoked via `os/exec` with explicit env (`DOCKER_BUILDKIT=1`,
`GIT_SSH_COMMAND=...`) and a timeout context. Stdout/stderr are piped line-by-line into the
log capture pipeline — no buffering of the whole output.

### Base image switch: distroless → debian-slim with git + docker CLI

Phase 1 ships `distroless/static`. That image has no git, no shell, no docker CLI — fine for a
single Go binary, blocking for Phase 2. We move to `debian-bookworm-slim` with `git`,
`docker-ce-cli`, and `docker-compose-plugin` installed via apt. The Go binary still runs as a
non-root user that is added to the `docker` group inside the container; the docker socket is
bind-mounted from the host.

This adds ~70 MB to the image and is the single biggest deviation from Phase 1. There is no
distroless path that ships docker — the Docker CLI itself requires libc, network tools, and
TLS roots.

### In-memory deployment job queue

A buffered channel and a single worker goroutine. One deployment per app at a time, FIFO
across apps. Persisted state lives in the `deployments` table, so on restart any row stuck in
`queued`, `building`, or `deploying` is marked `interrupted` and the queue starts empty.

Multiple parallel workers are out of scope: build I/O is heavy, the typical VPS won't run
more than one usefully, and the resulting state machine is much simpler.

### One SSH key pair per app

Per `mvp.md` § Repository Connection. Generated lazily on first SSH-URL deploy or first
"Test connection" call — not at app creation, because public-HTTPS repos never need a key.
Public key is plain-text in the DB (it's public); private key is sealed with
`crypto.Box` using `VAC_MASTER_KEY`.

Deferred: the onboarding wizard mentions a "global" deploy key (mvp.md § Onboarding Step 3).
We do not implement a global key in Phase 2 — the per-app key model fully covers MVP exit
criteria. The wizard step in Phase 5 can either reuse the per-app endpoint or be a thin
convenience layer over it.

### `.env` synthesis location

The `.env` file is written into `{VAC_WORK_DIR}/{slug}/.env` **before** `docker compose
build` runs, and removed on shutdown of the deploy worker (best-effort `os.Remove`). It's
written with mode `0600`. The repo working tree is at `{VAC_WORK_DIR}/{slug}/repo/`; the
`.env` lives one directory up so it is never accidentally committed even if the user runs
`git status` in the working copy.

`docker compose` finds it because we invoke compose with
`--project-directory {VAC_WORK_DIR}/{slug}/repo` and `--env-file ../.env`.

---

## Library decisions

| Concern | Pick | Why |
|---|---|---|
| Docker Engine API | `github.com/docker/docker/client` | Used for `docker events` stream and container inspect; the official client is the only realistic option. |
| SSH key generation | `crypto/ed25519` (stdlib) + `golang.org/x/crypto/ssh` | ED25519 keys, OpenSSH wire-format encoding for `authorized_keys` and private key PEM. |
| Compose YAML parsing | `gopkg.in/yaml.v3` (already in `go.mod`) | We only need a shallow parse — top-level `services:` keys and per-service `ports:` / `image:` / `build:`. No need for full Compose schema. |
| Process supervision | `os/exec` + `bufio.Scanner` | One scanner per stdout/stderr; lines flushed to the log writer with timestamps. |
| Job queue | stdlib channel | One buffered chan + one worker goroutine, no external queue. |
| Cron-like ticker | `time.NewTicker` | Retention prune runs on a fixed-interval ticker keyed off midnight in the configured TZ. |

**Not adopting:**

- `go-git/go-git` — see "Shell out to git" rationale above.
- `compose-spec/compose-go` — full schema parser is overkill; we read three fields.
- `robfig/cron` or other cron parsers — one nightly job does not need a parser.

---

## File layout (end of Phase 2)

```
api/
├── main.go                            # bootstrap (unchanged shape; wires Phase 2 services)
├── Dockerfile                         # rewritten: debian-slim base with git + docker CLI
├── migrations/
│   ├── 00005_ssh_keys.sql
│   ├── 00006_services.sql
│   ├── 00007_deployments.sql
│   ├── 00008_deployment_logs.sql
│   ├── 00009_runtime_logs.sql
│   ├── 00010_env_vars.sql
│   └── 00011_apps_status_widen.sql    # widen the CHECK constraint to the new status set
└── internal/
    ├── sshkey/
    │   ├── sshkey.go                  # ED25519 generation, OpenSSH formatting
    │   └── sshkey_test.go
    ├── gitcli/
    │   ├── gitcli.go                  # Clone, Pull, LsRemote, with SSH command injection
    │   └── gitcli_test.go
    ├── compose/
    │   ├── detect.go                  # find compose.yaml / docker-compose.yml / wrap Dockerfile
    │   ├── parse.go                   # shallow YAML parse for service names + ports
    │   ├── wrap.go                    # generate minimal compose.yaml for Dockerfile-only repos
    │   └── compose_test.go
    ├── dockercli/
    │   ├── compose.go                 # Build, Up, Down, Ps wrappers around `docker compose`
    │   ├── engine.go                  # docker/docker/client wrapper: events, inspect, prune
    │   └── dockercli_test.go
    ├── deploy/
    │   ├── pipeline.go                # orchestrator: clone → detect → build → up → health
    │   ├── worker.go                  # in-memory queue + single worker goroutine
    │   ├── status.go                  # status state machine + derivations
    │   ├── healthcheck.go             # HTTP GET with retries against exposed port
    │   ├── envfile.go                 # render .env from encrypted EnvVars
    │   └── pipeline_test.go
    ├── crashloop/
    │   ├── monitor.go                 # docker events subscriber + per-container counters
    │   └── monitor_test.go
    ├── retention/
    │   └── pruner.go                  # nightly goroutine, deletes by retention windows
    ├── store/
    │   ├── ssh_keys.go
    │   ├── services.go
    │   ├── deployments.go
    │   ├── deployment_logs.go
    │   ├── runtime_logs.go
    │   └── env_vars.go
    └── server/handler/
        ├── ssh_keys.go                # GET/POST/DELETE /api/apps/:id/ssh-key
        ├── test_connection.go         # POST /api/apps/:id/test-connection
        ├── deployments.go             # GET/POST /api/apps/:id/deployments + GET :did
        ├── stack_control.go           # start, stop, restart, per-service restart
        ├── env.go                     # GET/PUT /api/apps/:id/env
        └── services.go                # GET /api/apps/:id/services, PATCH per-name
```

`internal/store/` stays thin. Pipeline orchestration goes in `internal/deploy/` and only
calls into store. No service layer yet — handlers call `deploy.Enqueue(...)` and `store.Foo`
directly, refactor when something repeats.

---

## Data model additions

### `ssh_keys` (00005)

```
id              UUID PK
app_id          UUID UNIQUE NOT NULL REFERENCES apps(id) ON DELETE CASCADE
public_key      TEXT NOT NULL                 -- "ssh-ed25519 AAAA... vac-{slug}"
private_key     BYTEA NOT NULL                -- sealed with crypto.Box
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

`app_id` is UNIQUE because we issue exactly one key per app. The existing
`apps.git_ssh_key_id` column becomes redundant — drop it in this migration.

### `services` (00006)

```
id              UUID PK
app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
service_name    TEXT NOT NULL                 -- compose service key
container_id    TEXT                          -- last known docker container ID
exposed_port    INT                           -- host-facing port for health checks
domain          TEXT                          -- placeholder; Caddy wiring in Phase 3
status          TEXT NOT NULL DEFAULT 'created'
restart_count   INT NOT NULL DEFAULT 0
last_exit_code  INT
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
UNIQUE (app_id, service_name)
```

`status` is **not** constrained by a CHECK — we manage the enum in Go (`internal/deploy/
status.go`). Postgres CHECKs on enum strings are painful to migrate; the Go side validates
on write.

### `deployments` (00007)

```
id              UUID PK
app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
status          TEXT NOT NULL DEFAULT 'queued'
triggered_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
started_at      TIMESTAMPTZ
finished_at     TIMESTAMPTZ
compose_hash    TEXT                          -- sha256 of the resolved compose file
commit_sha      TEXT
commit_message  TEXT
error           TEXT                          -- single string, the step that failed
```

Index on `(app_id, triggered_at DESC)` for the history list.

### `deployment_logs` (00008)

Build logs from `docker compose build`. Permanent — kept with the deployment per mvp.md §
Log Retention.

```
id              BIGSERIAL PK
deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE
service_name    TEXT                          -- nullable; pipeline-level lines like "git clone"
stream          TEXT NOT NULL                 -- 'stdout' | 'stderr' | 'system'
message         TEXT NOT NULL
ts              TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

Batched inserts: the log writer buffers up to 200 lines or 250ms and flushes in one
multi-row INSERT to avoid hammering the DB per line.

### `runtime_logs` (00009)

Container stdout/stderr captured by `docker logs --follow`. Pruned by retention.

```
id              BIGSERIAL PK
app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
service_name    TEXT NOT NULL
stream          TEXT NOT NULL                 -- 'stdout' | 'stderr'
message         TEXT NOT NULL
ts              TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

Index on `(app_id, ts DESC)`. Phase 2 captures runtime logs to DB only (no WebSocket yet);
the same write path will fan-out to subscribers in Phase 4.

### `env_vars` (00010)

```
id              UUID PK
app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
key             TEXT NOT NULL
value           BYTEA NOT NULL                -- sealed with crypto.Box
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
UNIQUE (app_id, key)
```

### `apps.status` widening (00011)

Phase 1 has `CHECK (status IN ('created', 'stopped', 'running', 'deploying', 'failed'))`.
Phase 2 needs `building`, `degraded`, `crash-loop`, `error`, `interrupted`. Migration drops
the CHECK and relies on Go-side validation, matching how `services.status` is handled.

---

## Sequence

### M1 — Image base swap + docker socket access

**Goal:** the existing Phase 1 binary still works, but the image now has `git`, `docker`,
and `docker compose` available on `PATH`, and the docker socket is bind-mounted in dev.

- Rewrite `api/Dockerfile` runtime stage to `debian:bookworm-slim`:
  - `apt-get install -y ca-certificates git curl gnupg`
  - Add Docker apt repo, install `docker-ce-cli` and `docker-compose-plugin` (CLI only —
    no daemon inside the container)
  - Add a non-root `vac` user with UID 1000, GID matching the host `docker` group's GID at
    runtime (passed via `--group-add`)
- Update `compose.yaml` at repo root:
  - Mount `/var/run/docker.sock:/var/run/docker.sock`
  - Mount a named volume `vac_repos:/var/lib/vac/repos`
  - Add `group_add: ["${DOCKER_GID:-999}"]`
  - `.env.example` documents `DOCKER_GID` (Linux hosts) and notes Docker Desktop hosts can
    skip it
- Add a tiny health probe at boot: `exec.Command("docker", "version").Run()` — if it fails,
  log a single warning, don't crash. We want VAC to come up even on a misconfigured host so
  the operator can fix the socket from the UI.

**Test:** `make build && docker compose up` brings VAC up; `docker exec vac-api docker
version` and `docker exec vac-api git --version` both succeed; `/health` returns DB OK.

### M2 — Migrations for Phase 2 data model

**Goal:** all six new tables and the `apps.status` widening live in `migrations/`, applied
on boot.

- Add migrations 00005 through 00011 as specified in the Data Model section
- Drop `apps.git_ssh_key_id` in 00005 (per-app key is one-to-one)
- Write the store layer: `internal/store/{ssh_keys,services,deployments,deployment_logs,
  runtime_logs,env_vars}.go` — basic CRUD only, no joins
- Extend `store_integration_test.go` with one round-trip test per new table

**Test:** integration — for each new table, insert a fixture, read it back, assert fields
including encrypted-blob round trip through `crypto.Box`.

### M3 — SSH key generation + persistence

**Goal:** the API can mint an ED25519 key pair for an app, store it sealed, and surface the
public half.

- `internal/sshkey/sshkey.go`:
  - `Generate() (pub string, privPEM []byte, err error)` — `ed25519.GenerateKey`, encode
    the public key as `ssh-ed25519 BASE64 vac-key-{shortuuid}` and the private key as
    OpenSSH PEM (use `golang.org/x/crypto/ssh.MarshalPrivateKey` — already an indirect dep
    via `pquerna/otp`'s transitive tree; if not, add it directly)
  - Lazy creation pattern in the handler: if the app has no `ssh_keys` row and the git URL
    is SSH, mint one on demand
- Handlers (`internal/server/handler/ssh_keys.go`):
  - `GET /api/apps/:id/ssh-key` — returns `{ public_key, fingerprint, created_at }`,
    minting one if absent
  - `POST /api/apps/:id/ssh-key/regenerate` — issues a new key, replaces the row
  - `DELETE /api/apps/:id/ssh-key` — removes the row (for repos that switched to HTTPS)
- Wire routes under the auth-required `apps` group in `server.go`

**Test:** unit — round-trip a generated key through `crypto.Box`, parse it back with
`ssh.ParsePrivateKey`, confirm the public key matches. Handler test — `GET` on an SSH-URL
app mints; second `GET` returns same key.

### M4 — Git CLI wrapper + test-connection endpoint

**Goal:** users can verify their SSH deploy-key setup before they ever click Deploy.

- `internal/gitcli/gitcli.go`:
  - `LsRemote(ctx, url, branch string, sshKeyPath string) error` — wraps `git ls-remote
    --exit-code <url> <branch>`. Sets `GIT_SSH_COMMAND="ssh -i {sshKeyPath} -o
    StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null -o
    BatchMode=yes"` when sshKeyPath is non-empty. `BatchMode=yes` is critical — we never
    want git to prompt for a password.
  - `Clone(ctx, url, dest, branch, sshKeyPath string) error`
  - `Pull(ctx, dest, branch, sshKeyPath string) error` — `git -C dest fetch origin
    <branch> && git -C dest reset --hard origin/<branch>` (we treat the worktree as
    disposable; no merges)
  - Each function returns a typed error: `ErrAuth` (exit 128 + "Permission denied"),
    `ErrRepoNotFound` (exit 128 + "not found"), `ErrBranchNotFound`, or wrapped raw error
- The SSH key is materialised on disk into a temp file under `os.TempDir()/vac-ssh-*` with
  mode `0600`, written from `crypto.Box.Open(privateKey)`, and deleted on function return
  with `defer os.Remove`. The git process inherits the path via `GIT_SSH_COMMAND` and never
  touches the DB
- Handler `POST /api/apps/:id/test-connection`:
  - Look up the app and (if SSH URL) the key; if SSH URL and no key, mint one
  - Call `gitcli.LsRemote`, translate typed errors into structured 200 responses:
    `{ ok: bool, error_code: "auth_failed" | "repo_not_found" | "branch_not_found" |
    "network" | "other", error_message: string }`
  - Always return 200 — this is a probe, not a CRUD op; HTTP errors would be confusing

**Test:** integration against a real public GitHub repo for the HTTPS happy path; unit test
exercises each typed-error mapping using a fake `gitcli` interface.

### M5 — Compose detection + Dockerfile auto-wrap

**Goal:** given a cloned repo, decide which compose file to use, generating one if needed.

- `internal/compose/detect.go`:
  - `Detect(repoDir string) (Result, error)` where `Result` is one of:
    `{ Source: "compose.yaml", Path: "..." }`,
    `{ Source: "docker-compose.yml", Path: "..." }`,
    `{ Source: "generated", Path: "..." }`, or error `ErrNoComposeOrDockerfile`
  - Detection order: `compose.yaml`, `docker-compose.yml`, then check for `Dockerfile`
  - If only `Dockerfile`: call `compose.Wrap(repoDir)` which writes `compose.yaml` (the
    generated content from `mvp.md` § Deployment Model) into `{workdir}/{slug}/generated/`
    — never into the repo working tree
- `internal/compose/parse.go`:
  - `Parse(path string) ([]Service, error)` returning `{ Name, Ports []int, HasBuild bool,
    Image string }` for each service. Shallow `yaml.v3` decode into
    `map[string]map[string]any`; we only read three fields
- `internal/compose/wrap.go` writes the static template
- `.dockerignore` check: `WarnIfMissingDockerignore(repoDir) string` — returns the warning
  text or empty; called by the pipeline and logged into `deployment_logs`

**Test:** unit — fixtures for each of the three repo shapes (compose, docker-compose,
Dockerfile-only) + the missing-everything case. `compose.Parse` is exercised on a
representative multi-service compose with build, image, and ports variants.

### M6 — Docker CLI wrapper

**Goal:** typed Go functions wrap `docker compose build / up / down / ps` and stream output.

- `internal/dockercli/compose.go`:
  - `Build(ctx, projectDir, composeFile, projectName string, env []string, out
    io.Writer) error` — invokes `DOCKER_BUILDKIT=1 docker compose -p <projectName> -f
    <composeFile> build --progress plain`, scans stdout+stderr line by line, calls
    `out.Write` with each timestamped line. `--progress plain` gives parseable output
    instead of TTY animations.
  - `Up(ctx, projectDir, composeFile, projectName string, env []string) error` —
    `docker compose -p <projectName> -f <composeFile> up -d --remove-orphans`
  - `Down(ctx, projectName string, removeVolumes bool) error` — `--remove-orphans`,
    optional `-v`
  - `Ps(ctx, projectName string) ([]PsService, error)` — `docker compose ps --format json`,
    one JSON object per line, decoded into `{ Service, Name, State, Status, ExitCode,
    Health, Image, Publishers }`
- `internal/dockercli/engine.go`:
  - `Events(ctx) (<-chan events.Message, error)` — filtered to container events from
    `vac-*` projects (filter `label=com.docker.compose.project` prefix on the read side
    since the Docker filter API is awkward)
  - `Inspect(ctx, containerID string) (types.ContainerJSON, error)`
  - `ImageHistory(ctx, repoName string) ([]image.Summary, error)`
  - `RemoveImage(ctx, imageID string) error`
- The `out io.Writer` used for build is a `deploy.LogWriter` (see M7) — `dockercli`
  doesn't know about Postgres

**Test:** integration that runs against the host Docker daemon — build + up + ps + down on
a fixture compose project with a tiny `nginx:alpine` service. Skips if the daemon is
unavailable (mirrors the testcontainers `t.Skipf` pattern from M1).

### M7 — Deploy pipeline + queue + log writer

**Goal:** end-to-end deploy works on the happy path. Logs land in Postgres.

- `internal/deploy/pipeline.go` — `Run(ctx, deploymentID string) error`:

  ```
  loadAppAndKey
    → ensureWorkdir
    → clone-or-pull
    → setDeploymentStatus("cloning" → "building")
    → detectCompose
    → writeEnvFile (from encrypted env_vars)
    → checkDockerignore (warn-only)
    → dockerCompose.Build
    → setDeploymentStatus("deploying")
    → dockerCompose.Up
    → parseRunningServices + upsertServices
    → setDeploymentStatus("health-checking")
    → healthCheckEachService
    → setDeploymentStatus("running")
    → pruneOldImages
  ```

  On any step failure: `setDeploymentStatus("error")`, write `deployments.error` with the
  step name and message, leave the prior stack running (do not call `down`).

- `internal/deploy/worker.go`:
  - Buffered channel `chan string` (deployment IDs); single worker goroutine reads it
  - On startup: scan `deployments` for rows in `queued / building / deploying / health-
    checking` and mark them `interrupted` — this is the graceful-interrupt mechanism from
    mvp.md § Graceful Shutdown
  - `Enqueue(deploymentID)` appends to the channel; `POST /api/apps/:id/deployments`
    creates the row and enqueues
  - Worker respects the parent context — `ctx.Done()` exits the loop, the in-flight deploy
    finishes its current step then sees ctx.Err and marks itself `interrupted`

- `internal/deploy/envfile.go`:
  - `Render(ctx, store, box, appID, destPath) error` — read encrypted env vars, open them,
    write `KEY=VALUE\n` lines (no quoting — Docker Compose handles raw values), mode `0600`
  - Values that contain `\n` are written as `KEY="..."` with double-quote escaping

- `internal/deploy/loggers.go`:
  - `LogWriter` implements `io.Writer`, buffers up to 200 lines or 250ms, flushes via
    `store.AppendDeploymentLogs(deploymentID, []LogRow)`
  - Tagged with `service_name` parsed from `docker compose build` line prefixes
    (`#NN [stage_name] ...`) when possible; otherwise nil

**Test:** integration — create app pointing at a fixture repo (a `httpbin`-style
single-service Dockerfile), enqueue, wait for `running`, assert one service row, assert
build logs > 0, assert `pruneOldImages` ran. Run against the host Docker daemon, skip if
unavailable.

### M8 — Service detection, status model, stack control

**Goal:** the `services` table reflects reality and basic lifecycle endpoints work.

- `internal/deploy/status.go`:
  - Service-level transitions: `created → building → deploying → running → (degraded |
    crash-loop | stopped | error | interrupted)`
  - `DeriveAppStatus(services []Service) string`:
    - all `running` → `running`
    - any `crash-loop` → `crash-loop`
    - any `stopped` unexpectedly (i.e. no manual stop flag) → `degraded`
    - any `building` or `deploying` → mirror that
    - else → `error`
- `internal/deploy/services.go`:
  - `Upsert(ctx, appID string, ps []dockercli.PsService) error` — insert new, update
    existing (containerID, status mapped from docker State, exposed port from first
    Publisher), delete rows for services no longer in the compose project
- Handlers in `internal/server/handler/services.go` and `stack_control.go`:
  - `GET /api/apps/:id/services` — list with derived status
  - `PATCH /api/apps/:id/services/:name` — set `domain` (stored but unused until Phase 3)
    and `exposed_port`
  - `POST /api/apps/:id/start | stop | restart` — wrap `dockercli.compose` Up/Down with
    `--no-deps false` and an `--all` flag where appropriate
  - `POST /api/apps/:id/services/:name/restart` — `docker compose restart <name>`

**Test:** integration — deploy, list services, restart one, confirm new container ID in DB
and old containers exited cleanly.

### M9 — Health check

**Goal:** a service is not considered `running` until it answers HTTP on its exposed port.

- `internal/deploy/healthcheck.go`:
  - `Check(ctx, host string, port int, path string, retries int, timeout time.Duration)
    error` — `http.Get` against `http://{host}:{port}{path}`, retries with exponential
    backoff starting at 1s up to `retries` attempts, fails after `timeout`
  - Host is always `127.0.0.1` in Phase 2 — services publish on the host network via the
    `ports:` mapping in their compose file. Phase 3 will switch this to the Docker network
    once Caddy routes by service name
- Pipeline calls `Check` per service that has `exposed_port` set; services without an
  exposed port are considered passing automatically (they are background workers, queues,
  databases, etc., not HTTP services)
- Defaults from config: `VAC_HEALTH_CHECK_TIMEOUT=30s`, `VAC_HEALTH_CHECK_RETRIES=5`, path
  `/`. These already exist in `mvp.md` § Configuration — wire them through `config.Config`.

**Test:** unit — fake HTTP server that 503s 3 times then 200s; `Check` succeeds within
retries. Second test: server always 503s; `Check` returns `ErrHealthCheckFailed` after
exhausting retries.

### M10 — Crash-loop monitor

**Goal:** a continuously crashing container is stopped automatically and surfaced as
`crash-loop` in the API.

- `internal/crashloop/monitor.go`:
  - `Run(ctx)` — subscribe to `dockercli.engine.Events`, filter for container `die` events
    on containers labelled with `com.docker.compose.project=vac-*`
  - Per-container rolling window: `[]time.Time` of die timestamps trimmed to the last
    `VAC_CRASH_LOOP_WINDOW`. If count exceeds `VAC_CRASH_LOOP_THRESHOLD`:
    - Look up the app and service from the container labels
    - Call `dockercli.compose.Stop` on that single service (`docker compose -p <proj> stop
      <svc>`)
    - Set `services.status = 'crash-loop'`, capture `last_exit_code` from the event
    - Persist a row in `runtime_logs` flagged with `stream='system'`,
      `message='crash-loop: stopped after N restarts in M'` so it shows up alongside the
      service logs
  - Recovery is manual — the user must call `POST /api/apps/:id/services/:name/restart`
    after investigating logs (per mvp.md § Crash Loop Detection)
- Capture the last 50 lines of runtime_logs at stop time and store them under the
  service's last `deployment_id` for the UI to surface (per mvp.md). For Phase 2 they live
  in `runtime_logs`; Phase 5 wires the "preserved logs" UI affordance.

**Test:** unit — drive a synthetic event channel through `Run` and assert the stop call
fires at threshold + 1. Integration — deploy a service whose CMD is `exit 1`, observe
status flips to `crash-loop` within the window.

### M11 — Deployment history endpoints + env vars + retention

**Goal:** REST surface for the UI to read deploy history, manage env vars, and the
retention pruner is running in-process.

- Handlers:
  - `GET /api/apps/:id/deployments` — list, paginated by `triggered_at DESC`
  - `GET /api/apps/:id/deployments/:did` — full row including duration
  - `GET /api/apps/:id/deployments/:did/logs` — paginated build logs (cursor by `id`)
  - `GET /api/apps/:id/env` — list keys only (values masked); query param `?reveal=true`
    requires re-auth via password header (out of scope for Phase 2 — for now reveal is
    unimplemented; UI prompts a re-login in Phase 5)
  - `PUT /api/apps/:id/env` — replace-all: body is `{ "vars": { "KEY": "value", ... } }`,
    handler deletes prior rows and inserts the new set inside a transaction
- `internal/retention/pruner.go`:
  - Goroutine started from `main.go` after migrations
  - Loops on a ticker; calculates the next 03:00 in `time.Local`; sleeps until then; runs
    `DELETE FROM runtime_logs WHERE ts < NOW() - INTERVAL '7 days'` (interval value from
    config); same for activity if/when we add it
  - On `ctx.Done()` exits the loop
  - Logs a slog line with the row counts deleted

**Test:**
- Handler: round-trip env PUT then GET shows the new key set
- Unit on the pruner: pre-populate with old rows, call the prune function directly, assert
  row count drop

### M12 — Hardening pass

- `/health` already pings DB (Phase 1). Add a soft probe for the docker socket: a 1s
  timeout `docker version` call. Surface as `{"db":"ok","docker":"ok"}`; failure of docker
  drops to `503` since deployments can't run
- Image pruning verified: deploy 4 times to the same app, assert only the last
  `VAC_IMAGE_KEEP_COUNT` images remain via `dockercli.engine.ImageHistory`
- Error responses across new handlers follow the Phase 1 `errorBody` shape — review
- All `os/exec` calls have explicit `Env` (never `os.Environ()` plus appends — would leak
  `VAC_MASTER_KEY` into a child process)
- Run `golangci-lint run ./...` and fix findings
- Manual end-to-end: spin up the dev compose stack on a clean DB, run the setup wizard,
  create an app pointing at a known small public repo with a multi-service compose, click
  Deploy via curl, verify the service is reachable on its exposed port

---

## Testing strategy

| Layer | Tool | What it covers |
|---|---|---|
| Unit | `go test`, stdlib `httptest` | sshkey, compose detect/parse/wrap, status derivations, healthcheck retry logic, crashloop window counters, gitcli error classification, envfile rendering |
| Handler | `httptest.NewRecorder` + chi router | Per-endpoint: status codes, response shapes, validation rejections |
| Integration (DB) | `testcontainers-go` Postgres | Table round-trips for new tables, deployment row state transitions |
| Integration (Docker) | host Docker daemon, `t.Skipf` if absent | The pipeline happy path; build + up + ps + down on a fixture repo; crash-loop event handling |
| Manual | curl + the placeholder UI | End-to-end smoke before merging each milestone |

**Mocking policy unchanged from Phase 1:** no Postgres mocks. We add a Docker-integration
tier that *also* uses a real daemon — Compose's behaviour against a fake is too divergent to
trust. CI either runs against Docker-in-Docker or the tests skip with a clear message.

**Fixture repos:** keep two tiny Git repos in `api/internal/deploy/testdata/` —
`single-dockerfile/` and `multi-service-compose/`. Each is initialised in `TestMain` via
`git init` so the integration tests do not depend on the public internet.

---

## Configuration additions

These are documented in `mvp.md` § Configuration but become live in Phase 2:

| Variable | Default | First used by |
|---|---|---|
| `VAC_DOCKER_SOCKET` | `/var/run/docker.sock` | M1 image, M6 dockercli |
| `VAC_WORK_DIR` | `/var/lib/vac/repos` | M7 pipeline |
| `VAC_IMAGE_KEEP_COUNT` | `3` | M12 prune |
| `VAC_HEALTH_CHECK_TIMEOUT` | `30s` | M9 healthcheck |
| `VAC_HEALTH_CHECK_RETRIES` | `5` | M9 healthcheck |
| `VAC_CRASH_LOOP_THRESHOLD` | `5` | M10 monitor |
| `VAC_CRASH_LOOP_WINDOW` | `2m` | M10 monitor |
| `VAC_LOG_RETENTION_DAYS` | `7` | M11 pruner |
| `VAC_ACTIVITY_RETENTION_DAYS` | `30` | M11 pruner (placeholder; activity table lands later) |

Extend `internal/config/config.go` accordingly and add `vac.yaml` keys under `docker:`,
`deployments:`, and `logs:` per the schema in `mvp.md` § Configuration.

---

## Exit criteria

Phase 2 is done when all of these pass on a fresh clone:

- [ ] `make build` produces an image that includes `git`, `docker`, and `docker compose`
- [ ] `docker compose up` brings up vac-db + vac-api, `/health` returns `{"db":"ok","docker":"ok"}`
- [ ] `GET /api/apps/:id/ssh-key` mints a key the first time for an SSH-URL app
- [ ] `POST /api/apps/:id/test-connection` correctly returns `ok` for a reachable repo
      and `error_code: "auth_failed"` when the public key is not yet added to the host
- [ ] `POST /api/apps/:id/deployments` enqueues a deploy; the row moves through
      `queued → cloning → building → deploying → running` and ends `running`
- [ ] A repo with only `Dockerfile` is auto-wrapped and deploys
- [ ] A repo with a multi-service `compose.yaml` deploys with all services in `services` table
- [ ] Build logs are queryable via `GET /api/apps/:id/deployments/:did/logs`
- [ ] Env vars set via `PUT /api/apps/:id/env` are present inside the running container
      (test by deploying an `alpine` service whose CMD echoes a known var into a healthcheck)
- [ ] A service whose CMD is `exit 1` flips to `crash-loop` within
      `VAC_CRASH_LOOP_WINDOW` and stops
- [ ] After `VAC_IMAGE_KEEP_COUNT + N` deploys, only the most recent keep-count images per
      service remain
- [ ] Killing the api during a build leaves the deployment row marked `interrupted` on
      next boot (no `in_progress` ghosts)
- [ ] `runtime_logs` rows older than `VAC_LOG_RETENTION_DAYS` are deleted by the nightly
      prune (verified by setting a 1-minute retention in a test and waiting one tick)
- [ ] `golangci-lint run ./...` is clean
- [ ] Integration test suite passes locally (Postgres + Docker tiers)
- [ ] Control plane still idles under 200 MB RAM (excluding Postgres and user containers)
