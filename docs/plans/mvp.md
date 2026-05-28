# VAC MVP Plan

## Goal

A working self-hosted PaaS running on a single VPS where a developer can connect a Git repo
(single service or monorepo), deploy it as a Docker Compose stack with automatic HTTPS,
and observe it via a real-time dashboard.

Control plane target: under 200 MB RAM idle (excluding database).

---

## Scope

### In

- Connect Git repository via SSH deploy keys (public + private repos, any Git host)
- "Test connection" button to verify SSH key before first deploy
- Monorepo support via Docker Compose as the primary deployment model
- Single Dockerfile apps auto-wrapped into a minimal compose file internally
- Manual deploy trigger (click to deploy)
- Stack lifecycle: start, stop, restart, remove (per stack and per service)
- Richer service status model: building / deploying / running / degraded / crash-loop / stopped / error
- Crash loop detection with automatic stop and alert
- Automatic HTTPS via Caddy
- Automatic subdomains: `myapp.vac-domain.com` (zero DNS config per app)
- Custom domain support (one domain per exposed service)
- Environment variable management (stored encrypted at rest, injected into compose)
- `.env` file paste import
- Live deployment logs (streaming, per service)
- Live runtime logs (streaming, per service)
- Basic real-time stats per service: CPU, memory, uptime
- Dashboard: app list, deploy history, stack status, per-service resource usage
- Notifications: Discord and Slack webhooks for deploy events and crash loops
- Dark mode

### Out (post-MVP)

- GitHub / GitLab OAuth integration (repository browser, auto-webhook setup)
- Git push webhook auto-deploy
- Zero-downtime blue/green deploys
- Rollback
- Preview environments
- Managed Postgres / Redis provisioning (user runs their own via compose)
- Database backups (V2: user-defined backup commands, see below)
- Multi-node / remote builders
- Buildpack detection
- Teams / multi-user

---

## Repository Connection

VAC uses SSH deploy keys — no OAuth app registration, works with any Git host
(GitHub, GitLab, Gitea, Bitbucket, self-hosted).

### User flow

```
1. Create app in VAC → paste repo URL (git@github.com:user/repo.git)
2. VAC generates an ED25519 key pair, stores private key encrypted
3. UI displays the public key
4. User adds the public key as a deploy key in their repo settings
5. Click deploy — VAC clones using the private key
```

### Key management

- One SSH key pair generated per app (not shared across apps)
- Private key stored encrypted at rest (AES-256-GCM)
- Public key displayed in UI at any time for re-adding if needed
- Key deleted when app is deleted

### Public repos

For public repos the user can paste an HTTPS URL instead — no deploy key needed.
VAC detects the URL scheme and skips SSH key setup.

### Post-MVP

GitHub / GitLab OAuth integration as an optional enhancement — repository browser,
auto-webhook setup for push-to-deploy. SSH keys remain the universal fallback.

---

## Database & Stateful Services in Compose

Users may include any stateful service (Postgres, MySQL, Redis, etc.) directly in their
`compose.yaml`. VAC does not intercept, redirect, or manage these services — the user is
responsible for them.

### Volume persistence

`docker compose up --remove-orphans` removes orphaned containers but NOT named volumes.
Named volumes survive redeployments safely:

```yaml
services:
  db:
    image: postgres:16
    volumes:
      - db_data:/var/lib/postgresql/data   # safe across redeploys

volumes:
  db_data:
```

Anonymous volumes (`- /var/lib/postgresql/data`) are at risk of being orphaned.

**VAC will warn in the UI** if it detects a database-looking image (postgres, mysql, mongo,
mariadb, redis) using an anonymous volume mount.

### Backups (MVP)

VAC does not provide backups in the MVP. Users are responsible for their own data.
The UI will surface a visible warning on any app that has a stateful service with no
backup configured.

### Backups (V2 plan)

VAC will offer user-defined backup commands per service:

- User provides a shell command to run inside the container (e.g. `pg_dump -U $POSTGRES_USER $POSTGRES_DB`)
- VAC runs it via `docker exec` on a user-configured schedule
- Output is captured and shipped to a configured destination (S3, Backblaze B2, local volume)
- Credentials are sourced from the env vars already stored in VAC — no duplicate config

This approach is database-agnostic and requires no database-specific logic in VAC.
Volume-level backups (tarring the Docker volume) are intentionally avoided — they risk
inconsistent state unless the container is stopped.

---

## Deployment Model

Docker Compose is the **primary and only** deployment model.

| Repo type | How VAC handles it |
|---|---|
| Has `compose.yaml` | Used directly |
| Has `docker-compose.yml` | Used directly |
| Has only a `Dockerfile` | VAC generates a minimal `compose.yaml` internally and deploys that |
| Neither | Deployment fails with a clear error |

Auto-generated compose for single Dockerfile apps:

```yaml
services:
  app:
    build: .
    restart: always
    env_file:
      - .env
```

The user never sees or manages the generated file — it lives in VAC's working directory.

---

## Architecture

```
┌─────────────────────────────────────────┐
│               VPS Host                  │
│                                         │
│  ┌──────────┐    ┌──────────────────┐   │
│  │ vac-api  │    │   vac-worker     │   │
│  │  (Go)    │───▶│   (Go)           │   │
│  └──────────┘    └────────┬─────────┘   │
│        │                  │             │
│        │           Docker socket        │
│        │                  │             │
│  ┌─────▼──────┐   ┌───────▼─────────┐  │
│  │  vac-db    │   │  Docker Engine  │  │
│  │ (Postgres) │   │  user stacks    │  │
│  └────────────┘   └─────────────────┘  │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │  vac-proxy (Caddy)               │   │
│  │  dynamic config via Admin API    │   │
│  └──────────────────────────────────┘   │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │  vac-ui (React + shadcn SPA)   │   │
│  │  served as static files          │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### Components

| Component | Tech | Notes |
|---|---|---|
| `vac-api` | Go | REST API, WebSocket hub for real-time |
| `vac-worker` | Go | Deployment pipeline, same binary as api for MVP |
| `vac-proxy` | Caddy | Reverse proxy, auto HTTPS, dynamic config via Admin API |
| `vac-db` | PostgreSQL | Shared instance: VAC internal data + future managed user databases |
| `vac-ui` | React + TypeScript + Vite + shadcn/ui | SPA dashboard, embedded into api binary via `go:embed` |

### Static asset serving

The React build output (`dist/`) is embedded directly into the `vac-api` binary using
`go:embed`. No separate static file server, no path configuration, no extra compose service.
The result is a single deployable binary that serves both the API and the dashboard.

```go
//go:embed ui/dist
var staticFiles embed.FS
```

### Why shared Postgres over SQLite

`vac-db` is a shared Postgres instance intentionally designed to serve two purposes:

1. **Now (MVP):** VAC internal data — apps, deployments, env vars, logs, SSH keys. Lives in the `vac` database.
2. **Later (V2):** Managed user databases — each provisioned as a separate database on the same instance.

Starting with Postgres avoids a SQLite → Postgres migration when managed databases land.
With lean tuning (`shared_buffers=32MB`, `work_mem=2MB`) the instance idles at ~40–50 MB —
a one-time RAM cost paid regardless once V2 ships.

```
vac-db (Postgres instance)
  ├── database: vac          ← VAC internal: apps, deployments, logs, etc.
  ├── database: app_myapp    ← V2: managed DB for user app "myapp"
  └── database: app_blog     ← V2: managed DB for user app "blog"
```

---

## Data Model

### Apps

```
id, name, slug, git_url, git_branch, git_ssh_key_id,
compose_file (default: "compose.yaml"),
status, created_at, updated_at
```

### Services

One row per service declared in compose, configured by the user after first deploy detection:

```
id, app_id, service_name,
domain (nullable), exposed_port (nullable),
created_at, updated_at
```

### Deployments

```
id, app_id, status, triggered_at, started_at, finished_at,
compose_hash, error, commit_sha, commit_message
```

### EnvVars

```
id, app_id, key, value (encrypted), created_at, updated_at
```

### Logs

```
id, deployment_id (nullable), app_id, service_name (nullable),
source (build|runtime), message, timestamp
```

### SSHKeys

```
id, name, public_key, private_key (encrypted), created_at
```

### Users

```
id, username, password_hash (bcrypt),
totp_secret (encrypted, nullable), totp_enabled,
totp_recovery_codes (hashed array, nullable),
created_at, updated_at
```

### Sessions

```
id, user_id, token_hash, ip_address, user_agent,
created_at, expires_at, last_seen_at
```

### APITokens

```
id, user_id, name, token_hash, last_used_at, created_at, expires_at (nullable)
```

---

## API Surface

### Apps
```
GET    /api/apps
POST   /api/apps
GET    /api/apps/:id
PATCH  /api/apps/:id
DELETE /api/apps/:id
```

### Services
```
GET    /api/apps/:id/services               — list detected services + their config
PATCH  /api/apps/:id/services/:name         — set domain, exposed port
```

### Deployments
```
GET    /api/apps/:id/deployments
POST   /api/apps/:id/deployments            — trigger deploy
GET    /api/apps/:id/deployments/:did
```

### Environment Variables
```
GET    /api/apps/:id/env
PUT    /api/apps/:id/env                    — replace all
```

### Stack Control
```
POST   /api/apps/:id/start
POST   /api/apps/:id/stop
POST   /api/apps/:id/restart
POST   /api/apps/:id/services/:name/restart — restart single service
```

### Real-time
```
WS     /api/apps/:id/logs                   — all services, tagged by service name
WS     /api/apps/:id/services/:name/logs    — single service runtime logs
WS     /api/apps/:id/stats                  — all services stats, tagged by service name
WS     /api/deployments/:did/logs           — live build log stream
```

### SSH Keys
```
GET    /api/ssh-keys
POST   /api/ssh-keys
DELETE /api/ssh-keys/:id
```

### Misc
```
GET    /health                          — VAC health check (no auth required)
POST   /api/apps/:id/test-connection   — verify SSH key can reach the repo
```

### Auth
```
POST   /api/auth/login              — username + password → pre-auth or full session
POST   /api/auth/totp               — submit TOTP code after pre-auth
POST   /api/auth/logout             — revoke current session
GET    /api/auth/me                 — current user info
GET    /api/auth/sessions           — list all active sessions
DELETE /api/auth/sessions/:id       — revoke a specific session
DELETE /api/auth/sessions           — revoke all other sessions
POST   /api/auth/totp/setup         — initiate 2FA setup (returns QR + secret)
POST   /api/auth/totp/verify        — confirm setup with a code
DELETE /api/auth/totp               — disable 2FA (requires password confirmation)
GET    /api/auth/api-tokens         — list API tokens
POST   /api/auth/api-tokens         — create API token
DELETE /api/auth/api-tokens/:id     — revoke API token
```

---

## Deployment Pipeline

```
trigger deploy
    │
    ▼
clone / pull repo (git over SSH or HTTPS)
    │
    ▼
detect compose.yaml / docker-compose.yml
  → if missing and Dockerfile present: generate minimal compose.yaml
  → if neither: fail with clear error
    │
    ▼
write .env file from stored env vars into working directory
    │
    ▼
check for .dockerignore
  → if missing: log a warning, surface it in the UI build log
    ("No .dockerignore found — build context may be large")
    │
    ▼
docker compose build  [DOCKER_BUILDKIT=1 always enabled]
  (stream build logs per service → DB + WS clients)
    │
    ▼
docker compose up -d --remove-orphans
  - project name: vac-{app-slug}
  - compose network isolated from VAC internals
    │
    ▼
parse running services, upsert Services table
    │
    ▼
health check on each service that has a domain configured
  (HTTP GET / with retries, 30s timeout)
    │
    ▼
update Caddy config via Admin API
  - one route per service with domain → service:port
    │
    ▼
mark deployment success
prune old images (keep last 3 per service)
```

On any step failure: mark deployment failed, log error, leave previous stack running.

---

## Real-time Stats Architecture

Central collector — one goroutine per running container, fan-out to all subscribers.
Stats are tagged with `service_name` so the UI can show per-service breakdowns.

```
Docker stats stream (per container)
    │
    ▼
stats collector goroutine
  (tags with app_id + service_name)
    │
    ▼
in-memory pub/sub hub
    │
    ├── WS client A
    ├── WS client B
    └── WS client C
```

Poll interval: 2 seconds. Metrics per service: CPU %, memory MB, network rx/tx, uptime.

---

## Real-time Logs Architecture

Same fan-out pattern, tagged by service name:

- Build logs: streamed from `docker compose build` output → DB + WS clients, tagged by service
- Runtime logs: `docker logs --follow` per container → DB (ring buffer, last 10k lines per service) + WS clients

---

## Log Retention

Logs in Postgres are pruned on a background schedule to prevent unbounded growth:

| Log type | Retention |
|---|---|
| Runtime logs | 7 days (configurable in Settings) |
| Deployment build logs | Permanent — kept with the deployment record |
| Activity feed events | 30 days |

Pruning runs nightly as a background goroutine in `vac-api`. Retention period is a global
setting, not per-app for MVP.

---

## Caddy Integration

VAC manages Caddy via its Admin API (`localhost:2019`):

- On deploy: add one route per service that has a domain configured
- On service domain change: update that route
- On app delete: remove all routes for the app
- Caddy handles ACME / Let's Encrypt automatically

Caddy runs with a base config; VAC only manages the dynamic route layer.
Multiple services in one app can each have their own domain and cert.

### Request metrics

Caddy ships a built-in Prometheus metrics endpoint at `localhost:2019/metrics`.
VAC scrapes it every 10 seconds and maps the `upstream` labels to app/service names
to produce the request rate stats shown on the dashboard and per-app overview.

No external Prometheus instance required — VAC scrapes and stores the aggregated
values directly into Postgres (rolling 24h window, 10s resolution).

---

## Service Status Model

Every service has one of the following statuses at any time:

| Status | Meaning |
|---|---|
| `building` | Image build in progress |
| `deploying` | `docker compose up` running, waiting for health check |
| `running` | All services healthy |
| `degraded` | Stack running but one or more services are down |
| `crash-loop` | Service restarted above threshold — stopped, requires attention |
| `stopped` | Manually stopped |
| `error` | Deployment failed |
| `interrupted` | Deployment was in progress when VAC restarted |

Stack status is derived from its services: `running` only if all services are `running`,
`degraded` if any service is `crash-loop` or stopped unexpectedly, etc.

---

## Crash Loop Detection

`restart: always` in compose will silently restart a crashing container indefinitely.
VAC monitors restart counts and intervenes:

- **Threshold:** 5 restarts within 2 minutes (configurable via `VAC_CRASH_LOOP_THRESHOLD` and `VAC_CRASH_LOOP_WINDOW`)
- **Action:** VAC stops the service, sets status to `crash-loop`, fires a notification
- **UI:** Service row shows `crash-loop` badge with restart count and last exit code
- **Recovery:** User must manually restart the service from the UI after investigating logs

The last 50 lines of logs before the crash are preserved and surfaced prominently in the UI
alongside the crash-loop status.

---

## Automatic Subdomains

When VAC itself runs on a domain (e.g. `vac.example.com`), it can automatically assign
subdomains to apps — `myapp.vac.example.com` — with no DNS config required per app.

- Requires a wildcard DNS record: `*.vac.example.com → VPS IP` (set once)
- Caddy handles the wildcard TLS certificate via ACME DNS challenge
- VAC assigns `{app-slug}.{vac-domain}` automatically on app creation
- Custom domain can be added on top — both work simultaneously
- Configured via `VAC_BASE_DOMAIN=vac.example.com` in the config

When `VAC_BASE_DOMAIN` is not set, automatic subdomains are disabled and custom domains
are required. This keeps VAC functional in local/VPN mode without a public domain.

---

## Notifications

VAC sends webhook notifications to Discord and/or Slack for key events.

### Events

| Event | Trigger |
|---|---|
| Deploy succeeded | Deployment completes successfully |
| Deploy failed | Deployment fails at any step |
| Crash loop detected | Service exceeds restart threshold |
| TLS certificate expiring | Certificate expires within 14 days |
| VAC restarted | Control plane comes back up after a restart |

### Channels

**Discord:** standard webhook URL, VAC sends an embed with colour-coded status,
app name, commit SHA + message, duration, and a link to the deployment.

**Slack:** incoming webhook URL, VAC sends a Block Kit message with the same content.

**Custom webhook (post-MVP):** arbitrary HTTP POST with a JSON payload.

### Configuration

Webhook URLs configured in Settings → Notifications. Per-channel toggles for which
events to receive. All fields optional — notifications are entirely opt-in.

Env var overrides for scripted setups:
- `VAC_NOTIFY_DISCORD_URL`
- `VAC_NOTIFY_SLACK_URL`

---

## Security Considerations

### Infrastructure
- Docker socket access: api and worker run as a non-root user in the `docker` group
- Env vars, SSH keys, TOTP secrets: encrypted at rest using AES-256-GCM with `VAC_MASTER_KEY`
- Each app's compose stack gets its own Docker network, isolated from VAC internals
- `.env` file written to a VAC-controlled working directory, not the repo clone

### Authentication

VAC uses **username + password** with **Postgres-backed session cookies**.
JWT is intentionally avoided — VAC is single-node and instant session revocation matters
(a compromised session = root access to the VPS via Docker socket).

**Login flow:**
1. User submits username + password
2. Server verifies bcrypt hash
3. If 2FA enabled: server issues a short-lived pre-auth cookie, redirects to TOTP prompt
4. User submits TOTP code — server verifies, issues full session cookie
5. Session stored in DB (`sessions` table), cookie is HttpOnly + Secure + SameSite=Strict

**Session properties:**
- Session token stored hashed in DB (SHA-256) — raw token only ever in the cookie
- Default expiry: 7 days (configurable via `VAC_SESSION_TTL`)
- `last_seen_at` updated on each request
- "Remember me" extends TTY to 30 days
- All active sessions visible in Settings → Security with IP, user agent, last seen
- Any session can be individually revoked; "revoke all other sessions" button available

### 2FA (TOTP)

- TOTP via RFC 6238 (Google Authenticator / Authy compatible)
- Setup: scan QR code → verify one code → save recovery codes
- TOTP secret stored encrypted with `VAC_MASTER_KEY`
- 10 single-use recovery codes generated at setup, stored hashed
- When `VAC_EXPOSURE=public`: 2FA setup is strongly recommended — dashboard shows a
  persistent warning if 2FA is not configured
- When `VAC_EXPOSURE=local`: 2FA is optional, no warning

### Rate limiting & brute force protection

- Login endpoint: 5 attempts per 15 minutes per IP
- After limit hit: exponential backoff, IP temporarily blocked
- Failed attempts logged with IP and user agent for audit trail

### CSRF protection

Sessions use cookies → all mutating API endpoints require a CSRF token.
Token is issued as a non-HttpOnly cookie on login, sent back as a request header
(`X-CSRF-Token`) — the double-submit cookie pattern, no server-side state needed.

### API tokens (programmatic access)

Separate from browser sessions. Used for future CLI, webhooks, CI integrations.
- Opaque random tokens, stored hashed in DB
- Named and optionally scoped (read-only vs full access) — scopes post-MVP
- Created and revoked in Settings → API Tokens
- Never expire by default but expiry date can be set
- Sent as `Authorization: Bearer <token>` header (not cookies)

### Exposure modes

Configured via `VAC_EXPOSURE`:

| Mode | Behaviour |
|---|---|
| `public` (default) | VAC sits behind Caddy on a public domain with HTTPS. Secure cookie flag enforced. 2FA warning shown if not configured. |
| `local` | VAC binds to `VAC_HOST` (e.g. Tailscale IP or `127.0.0.1`). Intended for VPN or SSH tunnel access. 2FA optional. No public cert required. |

In `local` mode the UI shows a "local network mode" badge in the header.
User apps are still served publicly through Caddy regardless of VAC's exposure mode.

---

## Graceful Shutdown

`vac-api` listens for `SIGTERM` (sent by Docker on `docker compose stop`) and:

1. Stops accepting new HTTP requests
2. Waits for in-flight HTTP requests to complete (10s timeout)
3. Sends a close frame to all active WebSocket clients so the UI can show "reconnecting..."
4. Flushes any buffered log writes to Postgres
5. Closes the database connection pool
6. Exits cleanly

Any deployment in progress when shutdown is received is marked as `interrupted` in the
database so the UI shows it clearly rather than leaving it stuck as `in_progress`.

---

## Build Phases

### Phase 1 — Foundation
- Project scaffolding (Go modules, React + TypeScript + Vite + shadcn/ui + TanStack Query/Router, Docker Compose for VAC itself)
- Database schema and migrations
- Basic REST API skeleton with graceful shutdown
- First boot detection + console onboarding output
- Authentication: username + password, bcrypt, session cookies, CSRF protection
- TOTP 2FA setup and verification
- Rate limiting on login endpoint
- Session management (list, revoke)
- API token management
- App CRUD
- `go:embed` wiring for static assets

### Phase 2 — Deployment Pipeline
- Git clone / pull (SSH + HTTPS)
- "Test connection" endpoint (git ls-remote before first deploy)
- Compose file detection + Dockerfile auto-wrap
- `.dockerignore` presence check + warning
- `docker compose build` with BuildKit enabled + log streaming
- `docker compose up` with env injection
- Service detection and upsert
- Service status model (building / deploying / running / degraded / crash-loop / stopped / error)
- Crash loop detection + automatic stop
- Basic health check
- Deployment history
- Log retention pruning (nightly background goroutine)

### Phase 3 — Reverse Proxy & HTTPS
- Caddy integration
- Automatic subdomain routing (`{app}.{VAC_BASE_DOMAIN}`)
- Dynamic route management via Admin API (per service)
- Custom domain support per service
- SSL certificate automation (including wildcard for automatic subdomains)
- Caddy metrics scraping (request rate → Postgres rolling window)

### Phase 4 — Real-time
- WebSocket hub
- Live build logs (tagged by service)
- Live runtime logs (tagged by service)
- Per-service stats streaming (CPU, memory, uptime)
- Notification dispatch (Discord + Slack webhooks)

### Phase 5 — Dashboard UI
- React + TypeScript + Vite + shadcn/ui + TanStack Query/Router SPA setup
- Dark mode toggle (persisted to localStorage)
- Console + UI onboarding wizard (first boot flow)
- Global dashboard (app list, activity feed, host stats)
- Global deployments page (build metrics, in-progress indicator, timeline)
- Log explorer page (cross-app, filterable)
- Per-app detail page (see UI Structure below)
- `.env` paste import on environment tab
- Settings pages (including Notifications)

### Phase 6 — Hardening
- Error handling and recovery
- Crash-loop UI (badge, last exit code, preserved logs)
- Old image pruning
- Basic input validation
- `/health` endpoint
- End-to-end test on real VPS with a monorepo

---

## Configuration

Everything in VAC has a sensible default and is overridable — at minimum via environment
variable. Nothing is hardcoded. UI settings are a convenience layer on top, not the only way.

### Precedence (lowest → highest)

```
hardcoded defaults → vac.yaml config file → environment variables → UI / database settings
```

- **Env vars** always win over the config file
- **UI/database settings** only exist for user-facing preferences (instance name, log retention, etc.)
- Secrets (`VAC_MASTER_KEY`, `VAC_ADMIN_TOKEN`) are env-var only — never stored in the config file or exposed in the UI

### Config file

Optional `vac.yaml` mounted into the container. Useful for values that are awkward as env vars.
If not present, VAC runs entirely from env vars and defaults.

```yaml
# vac.yaml — all fields optional, env vars override everything
server:
  port: 3000
  host: "0.0.0.0"

docker:
  socket: "/var/run/docker.sock"
  work_dir: "/var/lib/vac/repos"     # where repos are cloned
  image_keep_count: 3                # old images to keep per service

caddy:
  admin_url: "http://localhost:2019"
  metrics_scrape_interval: "10s"

stats:
  poll_interval: "2s"

deployments:
  health_check_timeout: "30s"
  health_check_retries: 5

logs:
  runtime_retention_days: 7
  activity_retention_days: 30
  ring_buffer_lines: 10000           # in-memory buffer per service
```

### Full environment variable reference

| Variable | Default | Notes |
|---|---|---|
| `VAC_MASTER_KEY` | — | Required. 32-byte hex. Encrypts env vars + SSH keys. |
| `VAC_ADMIN_TOKEN` | auto-generated | Override to set a known token on first boot. |
| `VAC_PORT` | `3000` | HTTP port for vac-api. |
| `VAC_HOST` | `0.0.0.0` | Bind address. |
| `VAC_DATABASE_URL` | — | Required. Postgres connection string. |
| `VAC_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket path. |
| `VAC_WORK_DIR` | `/var/lib/vac/repos` | Where repos are cloned and built. |
| `VAC_IMAGE_KEEP_COUNT` | `3` | Number of old images to keep per service. |
| `VAC_CADDY_ADMIN_URL` | `http://localhost:2019` | Caddy Admin API base URL. |
| `VAC_CADDY_METRICS_INTERVAL` | `10s` | How often to scrape Caddy metrics. |
| `VAC_STATS_POLL_INTERVAL` | `2s` | Container stats poll frequency. |
| `VAC_HEALTH_CHECK_TIMEOUT` | `30s` | Max time to wait for a service to become healthy. |
| `VAC_HEALTH_CHECK_RETRIES` | `5` | Retry attempts before marking health check failed. |
| `VAC_LOG_RETENTION_DAYS` | `7` | Runtime log retention (days). |
| `VAC_ACTIVITY_RETENTION_DAYS` | `30` | Activity feed retention (days). |
| `VAC_LOG_RING_BUFFER` | `10000` | In-memory log lines per service. |
| `VAC_CONFIG_FILE` | `` | Path to optional `vac.yaml`. |
| `VAC_EXPOSURE` | `public` | `public` or `local` — controls auth strictness and bind behaviour. |
| `VAC_SESSION_TTL` | `168h` | Browser session lifetime (7 days). |
| `VAC_SESSION_TTL_EXTENDED` | `720h` | "Remember me" session lifetime (30 days). |
| `VAC_LOGIN_RATE_LIMIT` | `5` | Max login attempts per window per IP. |
| `VAC_LOGIN_RATE_WINDOW` | `15m` | Rate limit window duration. |
| `VAC_BASE_DOMAIN` | `` | VAC's own domain for automatic `app.vac-domain.com` subdomains. |
| `VAC_CRASH_LOOP_THRESHOLD` | `5` | Restarts within window before marking crash-loop. |
| `VAC_CRASH_LOOP_WINDOW` | `2m` | Rolling window for crash loop detection. |
| `VAC_NOTIFY_DISCORD_URL` | `` | Discord webhook URL for notifications. |
| `VAC_NOTIFY_SLACK_URL` | `` | Slack incoming webhook URL for notifications. |

### What's also configurable via UI

These values can be set in env/config as defaults and overridden in the dashboard:

- Instance name and timezone (Settings → General)
- Log retention period (Settings → General)
- Per-app: RAM limit, health check path/interval, restart policy (App → Settings → Runtime)
- Notification webhooks (Settings → Notifications)

---

## Onboarding

### VPS Level (console)

On first `docker compose up`, VAC detects a fresh install (empty database, no admin token)
and prints to stdout before accepting any requests:

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  VAC — first boot
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Dashboard:  http://your-server-ip:3000

  Open the dashboard to create your admin account.
  ⚠  Set VAC_MASTER_KEY in your compose env to
     enable encryption before deploying any apps.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

- No credentials printed to stdout — account is created in the UI setup wizard
- `VAC_MASTER_KEY` is a user-provided env var in the compose file (32-byte hex)
- If `VAC_MASTER_KEY` is missing, VAC starts but disables app creation and shows a warning
- Until an admin account exists, the dashboard redirects directly to the setup wizard
  and no auth is required to reach that page (first-user bootstrap)

### UI Level (setup wizard)

After the user logs in with their token for the first time, the dashboard shows a 3-step
setup wizard instead of an empty app list.

**Step 1 — Create admin account**
Set username and password. Password strength meter shown. Redirects to login after submit —
no auto-login on first account creation (intentional: forces the user to verify credentials work).

**Step 2 — Instance setup**
Name your VAC instance, set timezone. If VAC's own domain isn't configured yet, show a
DNS checklist: "Point an A record to `your-server-ip` → VAC handles the cert automatically."

**Step 3 — SSH key**
VAC generates a global ED25519 deploy key. Shows the public key with a copy button and
links to GitHub / GitLab deploy key docs. User can skip if using only public repos.

**Step 4 — Deploy your first app**
Inline mini app-creation flow: paste repo URL, pick branch, set a domain. One "Deploy"
button leads directly into the live build log view.

Once all steps are complete (or skipped), the wizard is dismissed and never shown again.

---

## Frontend Stack

| Concern | Library |
|---|---|
| Framework | React 19 + TypeScript |
| Build tool | Vite |
| Component library | shadcn/ui |
| Charts | shadcn Charts (built on Recharts) |
| Routing | TanStack Router (file-based) |
| Server state + data fetching | TanStack Query |
| WebSocket | Native browser WebSocket, integrated with TanStack Query's cache |
| Styling | Tailwind CSS (via shadcn) |
| Dark mode | shadcn built-in, toggled via class, persisted to localStorage |

TanStack Query manages all REST data fetching with caching and background refetch.
WebSocket messages (logs, stats) are received in dedicated hooks that write directly
into the Query cache so components stay reactive without separate state management.

---

## UI Structure

### Global Dashboard

- Running app count, host CPU, host RAM, host disk
- App list: name, git origin, domain(s), status (running / degraded / stopped)
- Activity feed: "Deploy of app-name succeeded", "Service web crashed and restarted" — each entry links to the relevant deployment or app
- Container budget: max RAM allocated, disk used, container count vs limit

### Global Deployments Page

- Metrics: avg build time, deployments today, success rate, in-progress count
- In-progress panel: active deployment with current step indicator
  ```
  clone → build → up → health check → proxy → done
  ```
- Timeline: all recent deployments across all apps, with app name, commit, status, duration

### Log Explorer Page

- Cross-app log view
- Filters: app, service, time range, log level
- Real-time tail mode toggle
- Export current filtered view (plain text or JSON)

### Per-App Detail Page

Navigation tabs: **Overview · Services · Deploys · Logs · Environment · Settings**

#### Overview Tab

- Aggregate stats: total CPU %, total RAM, request rate, uptime
- CPU and memory line charts with range selector (1h / 6h / 24h)
  - Service selector: aggregate view or per-service lines (colour-coded)
- Services breakdown table:
  ```
  Name     Status    CPU     Memory   Uptime    Actions
  web      running   0.3%    128 MB   3d 4h     [restart] [stop]
  worker   running   1.2%    64 MB    3d 4h     [restart] [stop]
  db       running   0.1%    48 MB    3d 4h     [restart] [stop]
  ```
- Domains per service with TLS certificate status and expiry date
- Recent deployments (last 3, link to Deploys tab)

For single-service apps the services table shows one row — same structure, no special casing.

#### Services Tab

Full per-service management:

- Per-service card: status, CPU, memory, uptime, restart count
- Actions per service: restart, stop/start (with dependency warning if stopping a service others depend on), view logs (shortcuts to Logs tab filtered to that service)
- Domain and exposed port configuration per service
- Post-MVP: terminal button (exec into container via WebSocket)

Stop/start of individual services is allowed but shows a warning when other services in the stack depend on the one being stopped.

#### Deploys Tab

- "Deploy from HEAD" button — pulls latest commit from configured branch and deploys
- Deployment history list: commit SHA, commit message, triggered at, duration, status
- Each entry expands to show the step-by-step timeline and build logs
- Failed deployments show the error and which step it failed at

#### Logs Tab

- Real-time log stream, all services interleaved by default
- Service filter dropdown (all / web / worker / db / ...)
- Level filter (all / error / warn / info)
- Time range picker for historical logs
- Auto-scroll toggle
- Export button: exports current filtered view as plain text or JSON

#### Environment Tab

> Variables are encrypted at rest with the host master key and injected only when the
> container starts. Changes require a restart to take effect.

- Key/value editor with show/hide toggle per variable
- Save triggers a "Restart required" banner with one-click restart
- Paste `.env` file to bulk import — VAC parses and merges with existing variables

#### Settings Tab

- **General:** app name
- **Source:** repository URL, branch, autodeploy toggle
  - When autodeploy is enabled: show inbound webhook URL to add to Git host
- **Runtime:** RAM limit (overrides compose), health check path + interval + timeout, restart policy (always / on-failure / unless-stopped)
- **Danger zone:** stop app, delete app (with confirmation)

---

## VAC Self-Hosting (dogfooding)

VAC itself is deployed via Docker Compose for bootstrap:

```yaml
services:
  vac-api:
  vac-db:       # shared Postgres: vac internal + future managed user DBs
  vac-proxy:
```

Recommended lean Postgres config for small VPS:

```
shared_buffers = 32MB
work_mem = 2MB
max_connections = 50
```

User app stacks run outside this compose stack, managed via Docker socket,
each with their own isolated compose project and network.

---

## Success Criteria

MVP is done when:

- [ ] Connect a real monorepo with `compose.yaml` and deploy it
- [ ] Connect a single-service repo with only a `Dockerfile` and deploy it (auto-wrap)
- [ ] "Test connection" correctly reports SSH key success and failure before first deploy
- [ ] App is reachable at automatic subdomain `myapp.vac.example.com` after deploy
- [ ] Each exposed service is live at `https://service.example.com` with a valid cert
- [ ] Env vars are injected and accessible at runtime across all services
- [ ] Pasting a `.env` file imports variables correctly
- [ ] Live build logs appear in the UI per service during deploy
- [ ] Runtime logs stream in real-time, tagged by service name
- [ ] CPU and memory stats update live per service in the dashboard
- [ ] A crashing service is detected as crash-loop, stopped, and a Discord notification fires
- [ ] Services restart automatically on non-crash restarts
- [ ] Login, 2FA setup, and session revocation all work correctly
- [ ] Dark mode toggle works and persists
- [ ] VAC control plane idles under 200 MB RAM (excl. database)
- [ ] `/health` returns 200
