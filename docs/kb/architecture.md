<!-- generated from commit 2f520b8 on 2026-06-03 — regenerate with /refresh-kb; if HEAD has moved past this commit and api/internal/ or ui/src/ layout changed, treat as possibly stale -->

# Architecture — current state

VAC is a single-VPS PaaS made of three containers plus databases:

```
                         ┌──────────────────────────────────────────┐
   browser  ──HTTPS──▶   │  vac-proxy (Caddy)                        │
                         │  • automatic HTTPS (ACME HTTP challenge)   │
                         │  • reverse-proxies user apps via vac-edge  │
                         │  • admin API :2019 (vac-api drives it)     │
                         │  • JSON access log (tailed for req rate)   │
                         └───────────────┬──────────────────────────┘
                                         │ vac-edge network (alias {slug}--{service})
   browser  ──HTTPS──▶   ┌───────────────┴──────────────────────────┐      ┌──────────────┐
   (dashboard)          │  vac-api (Go)                              │──────│  Postgres 16 │
                        │  HTTP server (chi) + deploy worker pool    │ pgx  │  (vac-db)    │
                        │  controls Docker daemon + Caddy admin API  │      └──────────────┘
                        │  *** deliberately NOT on vac-edge ***      │
                        └───────────────┬──────────────────────────┘
                                        │ Docker socket
                        ┌───────────────┴──────────────────────────┐
                        │  user app containers (one compose stack   │
                        │  per app, project name vac-{slug});       │
                        │  HTTP services join vac-edge.             │
                        │  managed-DB add-ons run as shared per-     │
                        │  engine containers (e.g. vac-mariadb)     │
                        └───────────────────────────────────────────┘
```

`vac-api` is one binary that is **both** the HTTP API and the deploy worker pool (no separate
worker process in the MVP). The React UI is embedded into that binary via `go:embed`.

## Backend package map (`api/internal/`)

Each package owns one concern.

| Package | Responsibility |
|---|---|
| `server` | HTTP wiring (chi router + middleware); `server/handler/` holds the handlers (one file per resource) |
| `store` | PostgreSQL persistence (pgx/v5), all DB access; goose migrations live under `store/migrations/` |
| `db` | `pgxpool` connection pool + migration runner (`Open`) |
| `config` | env-var / `vac.yaml` configuration (`Load` → `Config`) |
| `auth` | sessions (`SessionManager`, SHA-256-hashed tokens), TOTP pre-auth, password, API-token auth |
| `crypto` | `crypto.Box` AES-256-GCM seal/open for secrets at rest |
| `deploy` | the deploy pipeline (`Pipeline`) + worker pool/queue (`Worker`) + reaper; status enums (`status.go`); build-log writer (`LogWriter`) |
| `adapter` | normalizes a build source (compose / Dockerfile / framework / static) to one compose file via `adapter.For` + `Prepare` |
| `compose` | shallow compose parse, `Detect` build type, `Wrap` a Dockerfile, `Preflight` lint, `WriteResourceOverride` (per-app RAM cap) |
| `dockercli` | thin wrappers over `docker`/`docker compose` (`Compose.Build/Up/Down/Ps/Exec`, `Events`) |
| `dockerevents` | single `docker events` stream fanned out to subscribers (`Bus`) with reconnect |
| `gitcli` | git `LsRemote`/`Clone` (shallow)/`Pull`/`HeadCommit`/`FetchCommit` via the deploy SSH key |
| `sshkey` | generate/store/decrypt ED25519 deploy keys (`Manager`, `Generate` → `KeyPair`) |
| `caddy` | Caddy admin-API client + config schema (`Config`, `Route`, `UpstreamStatus`) |
| `proxy` | `Manager` maps apps→Caddy routes; attaches containers to `vac-edge`; health gating |
| `crashloop` | `Monitor` watches `die` events, trips on N restarts in a window, stops the service |
| `logstream` | `Supervisor` tails `docker logs --follow` per container into the `runtime_logs` ring buffer |
| `stats` | per-app `docker stats` (`Manager`, subscriber-gated, live-only) + host stats (gopsutil) |
| `reqmetrics` | `Collector` scrapes/aggregates the Caddy access log into per-service request rate |
| `notify` | `Dispatcher` for Discord/Slack/webhook (deploy ok/fail, crash-loop, restarted, cert expiry) with SSRF guard |
| `retention` | nightly `Pruner`: runtime logs, request metrics, audit log, per-service image prune, deployment history |
| `webhook` | turns inbound Git webhooks into deploy decisions (per-app secret auth, `ParseRef` vs `deploy_triggers`) |
| `dbprovision` | provisions/deprovisions managed databases (`Engine` + per-engine recipes), yields connection strings |
| `addon` | `Installer` materializes catalog templates into an app (env defaults, `@random` secrets, DB provisioning, enqueue deploy) |
| `backup` | `Engine` runs a backup end-to-end: exec in container → stream to destination → record run → prune → notify |
| `revert` | `Reverter` applies the inverse of revertable audit entries (env replace, base-domain, app-config) from before-snapshots |
| `audit` | per-request mutable `Record` carried in context, enriched by handlers, persisted by middleware |
| `auditdiff` | computes normalized before→current diffs for curated audit entries (`FieldStatus`, secret masking) |
| `appspec` | portable VAC app spec (`vac.app.yaml`): `Spec` + `FromApp`/`ToApp` for import/export |
| `portability` | import on-ramp / export exit-ramp (`Export`/`Import`) wrapping `appspec` |
| `certprobe` | single TLS-leaf-cert observation (`Func`, reads `NotAfter`), shared by `certcheck`/`domainstatus` |
| `certcheck` | daily goroutine reading cert `NotAfter` for every managed host, fires expiry alerts |
| `domainstatus` | background reconciler + in-memory store of DNS/cert health for custom + auto domains |
| `security` | host security-posture monitor (`PostureFinding`: fail2ban, firewall, kernel settings) |
| `promexport` | renders VAC metrics as Prometheus text exposition from a `Snapshot` |
| `ws` | WebSocket `Hub`: topic-based pub/sub (`build:{id}`, `logs:…`, `stats:{appId}`, `host`, `deployments`) with first/last-subscriber hooks |
| `admin` | CLI subcommands (password reset, import/export) outside the HTTP stack |
| `ui` | `go:embed` of the built SPA (behind the `embedui` tag), index.html fallback |

## Frontend map (`ui/src/`)

- `features/` — one folder per dashboard area: `apps`, `app-detail`, `deployments`,
  `database`, `addons`, `activity`, `security`, `logs`, `onboarding`, `settings`.
- `components/` — shared UI: `auth/` (auth shell), `layout/` (app-shell, sidebar, command
  menu), `theme/` (provider + toggle), `common/` (stat-tile, meter, status-pill, log-viewer,
  empty-state, …), `ui/` (the shadcn/Radix primitive kit).
- `lib/` — `api/` (typed client + per-resource modules), `ws/` (WebSocket hooks),
  `query/` (TanStack Query key factory), plus small utilities (`deploy-status`, `env-parse`,
  `format`, `log-export`, `service-color`, `use-document-title`, `utils`).
- `routes/` + `routeTree.gen.ts` — TanStack Router file-based routes. **`routeTree.gen.ts` is
  generated; don't hand-edit.**

## Data model (Postgres)

Tables live in `api/internal/store/migrations/`, grouped by concern:

- **Auth:** `users`, `sessions`, `api_tokens`.
- **Apps & services:** `apps` (includes `webhook_secret_enc`), `services`, `ssh_keys`,
  `env_vars`, `domains` (custom/auto hosts, cert expiry, redirects, lifecycle).
- **Deploy:** `deployments`, `deployment_logs`, `deploy_triggers` (push-to-deploy rules).
- **Observability:** `runtime_logs` (ring buffer), `request_metrics` (10s buckets).
- **Config:** `instance_settings` (singleton: base domain, `max_concurrent_deploys`),
  `notification_settings`.
- **Managed services:** `managed_databases` (app-owned DBs on shared engines),
  `backup_configs` + `backup_runs`, `addon_installs`.
- **Audit:** `audit_log` (every mutating action: actor, target, summary, metadata, status).

Encrypted-at-rest columns (sealed with `crypto.Box`, need `VAC_MASTER_KEY`): `env_vars`
values, `ssh_keys.private_key`, `users.totp_secret`, `notification_settings` Discord/Slack
URLs, `apps.webhook_secret_enc`, `managed_databases.secret_enc` (connection string + password),
and `backup_configs.dest_config` (S3 credentials JSON).

## Invariants

See the "Architecture invariants" section of `/CLAUDE.md` and `docs/deviations.md` for the
full rationale. The load-bearing ones:

1. `vac-api` is **off** `vac-edge`; consequently Caddy (not vac-api) owns deploy health.
2. Routing is by deterministic DNS alias `{slug}--{service}` on `vac-edge`, no host ports for
   HTTP services.
3. Secrets encrypted at rest; cookies `Secure` per-request; deploy failure never tears down
   the running stack.
