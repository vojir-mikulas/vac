<!-- generated from commit 0f94e36 on 2026-05-31 — regenerate with /refresh-kb; if HEAD has moved past this commit and api/internal/ changed, treat as possibly stale -->

# Architecture — current state

VAC is a single-VPS PaaS made of three containers plus a database:

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
                        │  HTTP server (chi) + deploy worker         │ pgx  │  (vac-db)    │
                        │  controls Docker daemon + Caddy admin API  │      └──────────────┘
                        │  *** deliberately NOT on vac-edge ***      │
                        └───────────────┬──────────────────────────┘
                                        │ Docker socket
                        ┌───────────────┴──────────────────────────┐
                        │  user app containers (one compose stack   │
                        │  per app, project name vac-{slug})        │
                        │  HTTP services join vac-edge              │
                        └───────────────────────────────────────────┘
```

`vac-api` is one binary that is **both** the HTTP API and the deploy worker (no separate
worker process in the MVP). The React UI is embedded into that binary via `go:embed`.

## Backend package map (`api/internal/`)

Each package owns one concern.

| Package | Responsibility |
|---|---|
| `server` | HTTP wiring; `server/handler/` holds the chi handlers (one file per resource) |
| `store` | PostgreSQL persistence (pgx/v5), goose migrations, all DB access |
| `deploy` | the deploy pipeline + worker queue; status enums; build-log writer |
| `compose` | detect compose vs Dockerfile; auto-wrap a Dockerfile into a minimal compose file |
| `dockercli` | thin wrappers over `docker compose` and `docker network` commands |
| `dockerevents` | single `docker events` stream fanned out to subscribers (bus) |
| `gitcli` | git clone/pull/ls-remote/head-commit using the deploy SSH key |
| `sshkey` | generate/store/decrypt ED25519 deploy keys |
| `caddy` | Caddy admin-API client + config schema (routes, upstreams, base config) |
| `proxy` | maps apps→Caddy routes; attaches containers to `vac-edge`; health gating |
| `crashloop` | watches `die` events, trips on N restarts in a window, stops the service |
| `logstream` | tails `docker logs --follow` per container into the runtime-log ring buffer |
| `stats` | per-app `docker stats` (subscriber-gated, live-only) + host stats (gopsutil) |
| `reqmetrics` | scrapes/aggregates Caddy access log into per-service request rate |
| `notify` | Discord/Slack webhook dispatch (deploy ok/fail, crash-loop, restarted) |
| `retention` | nightly cleanup: runtime logs, request metrics, audit log, per-service image prune, deployment history |
| `crypto` | `crypto.Box` AES-256-GCM encrypt/decrypt for secrets at rest |
| `auth` | sessions, TOTP, password, API-token auth |
| `config` | env-var parsing / configuration |
| `ws` | WebSocket hub: topic-based pub/sub (`build:{id}`, `logs:…`, `stats:{appId}`, `host`) |
| `db` | connection/pool + migration runner |
| `admin` | CLI subcommands (e.g. password reset) |
| `ui` | `go:embed` of the built SPA (behind the `embedui` tag) |

## Frontend map (`ui/src/`)

- `features/` — one folder per dashboard area: `apps`, `app-detail`, `deployments`,
  `database`, `settings`.
- `components/` — shared UI (auth, layout, theme, the shadcn/Radix-based kit).
- `lib/` — `api/` (typed client), `ws/` (WebSocket), `query/` (TanStack Query setup), plus
  small utilities (`env-parse`, `format`, `service-color`, `log-export`).
- `routes/` + `routeTree.gen.ts` — TanStack Router file-based routes. **`routeTree.gen.ts` is
  generated; don't hand-edit.**

## Data model (Postgres)

Core tables (see `api/internal/store/`): `users`, `api_tokens`, `apps`, `services`,
`domains`, `deployments`, `deployment_logs`, `runtime_logs`, `env_vars`, `ssh_keys`,
`notification_settings`, `request_metrics`. Encrypted columns (env values, SSH private keys,
TOTP secrets, webhook URLs) are sealed with `crypto.Box` and need `VAC_MASTER_KEY`.

## Invariants

See the "Architecture invariants" section of `/CLAUDE.md` and `docs/deviations.md` for the
full rationale. The load-bearing ones:

1. `vac-api` is **off** `vac-edge`; consequently Caddy (not vac-api) owns deploy health.
2. Routing is by deterministic DNS alias `{slug}--{service}` on `vac-edge`, no host ports for
   HTTP services.
3. Secrets encrypted at rest; cookies `Secure` per-request; deploy failure never tears down
   the running stack.
