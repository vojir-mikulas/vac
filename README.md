# VAC

**A self-hosted PaaS for a single VPS.** Connect a Git repo, and VAC clones it, builds it as a
Docker Compose stack, fronts it with Caddy for automatic HTTPS, and shows it all on a real-time
dashboard — live logs, deploys, container health, and CPU/RAM. One operator, one box, no
Kubernetes.

Built to stay out of your way: **< 200 MB RAM idle** (excluding the database).

> [!NOTE]
> VAC is young and moving fast. It's usable, but expect rough edges — issues and feedback welcome.

## Features

- **Git → live in one click** — point VAC at a repo; it clones, builds the Compose stack, and routes traffic.
- **Automatic HTTPS** — Caddy provisions Let's Encrypt certs; every app gets an `{app}.{domain}` subdomain.
- **Real-time dashboard** — live build & runtime logs, deploy timeline, container health, per-service and host CPU/RAM, all over WebSocket.
- **Safe deploys** — health-gated rollouts; a failed deploy never tears down the running version (it keeps serving while failure is recorded as state).
- **Secrets encrypted at rest** — env vars, SSH deploy keys, TOTP secrets, and webhook URLs sealed with AES-256-GCM.
- **Auth with 2FA** — operator login with TOTP and step-up for sensitive actions.
- **Managed add-ons** — optional managed databases, backups, and an add-on catalog.
- **Notifications** — deploy and health events to Discord / Slack.

## Quickstart

On a fresh Linux VPS with Docker installed:

```sh
curl -sSL get.vac.vojir.io | sh
```

The installer walks you through a short setup (domain, managed services, sudo-free access) and
shows a summary before touching the host. Piped without a terminal it runs unattended with safe
defaults. Re-running upgrades the images and preserves your secrets and config.

```sh
# Pin a version and domain up front (skips the matching prompts):
VAC_VERSION=v0.5.0 VAC_DOMAIN=vac.example.com sh install.sh
```

Without a domain, the dashboard is reachable at `http://<host>:9393`. With `VAC_DOMAIN` set, it
moves to `https://vac.<domain>` and each app gets an automatic `{app}.{domain}` subdomain over
HTTPS. See [`.env.example`](.env.example) for all configuration knobs.

## Architecture at a glance

```
┌─────────┐   git    ┌──────────────────────────┐   routes   ┌───────────┐
│  Repo   │ ───────► │  vac-api (Go)            │ ◄────────► │ vac-proxy │ ─► your apps
└─────────┘          │  HTTP server + deploy    │   health   │  (Caddy)  │    (Compose
                     │  worker in one binary    │            └───────────┘     stacks)
                     └──────────────────────────┘
                                  ▲
                                  │ embedded SPA (go:embed)
                     ┌──────────────────────────┐
                     │  ui (React 19 + Vite)    │
                     └──────────────────────────┘
```

- `api/` — Go backend (`vac-api`): HTTP server **and** deploy worker in one binary. Packages under `api/internal/`.
- `ui/` — React 19 + TypeScript SPA (Vite, TanStack Router/Query, Tailwind 4), embedded into the Go binary via `go:embed`.
- `proxy/` — Caddy reverse-proxy container (`vac-proxy`) — owns TLS and deploy health.
- `scripts/` — `install.sh` and operational scripts.
- `docs/` — `kb/` (current-state reference) and `plans/` (historical intent).

A few deliberate, non-obvious design decisions (e.g. the control plane stays off the app network;
Caddy owns deploy health) are documented in [`docs/deviations.md`](docs/deviations.md) and
[`docs/kb/`](docs/kb/).

## Development

Prerequisites: **Go 1.25+, Node 22+, pnpm 10+, Docker.**

```sh
pnpm install            # root dev hooks
pnpm --dir ui install   # UI deps
make dev                # Go API + Vite dev server; UI proxies /api → :3000
```

Common targets (see the [`Makefile`](Makefile) for the full list):

```sh
make build              # UI → embedded, then Go binary
make test               # unit tests (Go race + vitest)
make test-integration   # Go integration tests (needs Docker; testcontainers)
make lint               # golangci-lint + eslint + prettier
make typecheck          # tsc
```

## Contributing

Contributions are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md). To report a security
vulnerability, please follow [`SECURITY.md`](SECURITY.md) rather than opening a public issue.

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
