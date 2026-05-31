# CLAUDE.md

Guidance for AI agents (and humans) working in this repo. Keep it tight — this file
loads into context every session, so it pays for brevity. Deep detail lives in
[`docs/kb/`](docs/kb/); planning history lives in [`docs/plans/`](docs/plans/).

## What VAC is

A self-hosted PaaS for a **single VPS**: connect a Git repo, VAC clones it, builds it as a
Docker Compose stack, fronts it with Caddy for automatic HTTPS, and shows it on a real-time
dashboard (live logs, deploys, container health, CPU/RAM). Target footprint: **<200 MB RAM
idle** (excluding the database). One operator, one box.

## Repository layout

```
api/                 Go backend (vac-api) — HTTP server + deploy worker in one binary
  internal/          all backend packages (see docs/kb/architecture.md for the map)
  main.go            entrypoint, wiring, graceful shutdown
ui/                  React 19 + TypeScript SPA (Vite, TanStack Router/Query, Tailwind 4)
  src/features/      one folder per dashboard area (apps, app-detail, deployments, …)
proxy/               Caddy reverse-proxy container config (vac-proxy)
scripts/             install.sh and operational scripts
docs/
  kb/                AI-maintained knowledge base — CURRENT-STATE reference (see below)
  plans/             mvp.md + phase plans — HISTORICAL intent, NOT current truth
  deviations.md      honest log of where the build departs from mvp.md, with rationale
compose.yaml         local dev stack (vac-db + vac-api)
compose.prod.yaml    production stack (prebuilt GHCR images)
Makefile             all build/dev/test entrypoints
```

## Build, run, test

Prereqs: Go 1.25+, Node 22+, pnpm 10+, Docker.

```
make dev              # API (go run) + Vite dev server in parallel; UI proxies /api → :3000
make build            # UI → api/internal/ui/dist, then Go binary with embedded UI
make test             # unit tests (Go race + vitest); excludes integration
make test-integration # Go integration tests (needs Docker; testcontainers)
make lint             # golangci-lint + eslint + prettier
make typecheck        # tsc
make compose-up       # full stack via Docker Compose against a real Postgres
```

The UI is embedded into the Go binary via `go:embed` behind the `embedui` build tag — prod
builds bundle it; `make build-api-noembed` skips it for dev/test.

## Architecture invariants (the non-obvious bits)

These are deliberate decisions; don't "fix" them without understanding the trade-off
(documented in `docs/deviations.md`).

- **`vac-api` is deliberately NOT on the `vac-edge` network.** User app containers join
  `vac-edge`; the control plane stays off it so user code can't reach the API. This is why
  deploy health-gating goes through Caddy (vac-api can't hit app containers directly).
- **Caddy owns deploy health.** The pipeline gates `→ running` by polling Caddy's
  `/reverse_proxy/upstreams` admin endpoint, not by probing the container itself.
- **Routing is by DNS alias, not host ports.** Each HTTP service attaches to `vac-edge` with
  alias `{slug}--{service}`; Caddy routes to `{slug}--{service}:{internal_port}`. HTTP
  services do not publish host ports.
- **Secrets are encrypted at rest** with `crypto.Box` (AES-256-GCM): env vars, SSH deploy
  keys, TOTP secrets, notification webhook URLs. Requires `VAC_MASTER_KEY`.
- **Cookies are `Secure` per-request** (only when the request is actually TLS), not by a
  static config flag.
- **Deploy failure never tears down the running stack** — the prior version keeps serving;
  failure is recorded as state (`error`/`degraded`), not a rollback.

## Conventions

- Backend packages are single-responsibility under `api/internal/` (one concern each:
  `deploy`, `caddy`, `proxy`, `store`, `crashloop`, …). HTTP handlers live in
  `api/internal/server/handler/`, persistence in `api/internal/store/` (pgx + goose
  migrations). Tests sit next to code (`*_test.go`); integration tests are tagged
  `integration`.
- UI is feature-foldered under `ui/src/features/`; shared API client / WS / query setup in
  `ui/src/lib/`. Routing is file-based via TanStack Router (`routeTree.gen.ts` is generated —
  don't hand-edit).
- Before considering a change done, run `/code-review` (correctness) and `/simplify`
  (cleanup). See `docs/kb/conventions.md` for the end-to-end "add a feature" walkthrough.
- At the end of a milestone/phase, propose a ready-to-paste Conventional Commit message
  (commitlint-compatible — see `commitlint.config.js`).

## The knowledge base (`docs/kb/`) — read this, and keep it honest

`docs/kb/` is the **current-state** reference, regenerated from source rather than
hand-patched. Each file carries a provenance header (`generated from commit … on …`).

**Constraint for agents working in this repo:**
- **Trust source code over any doc.** If a KB file disagrees with the code, the code wins —
  and the KB file is stale and should be regenerated.
- **Treat `docs/plans/` as historical intent, not current behavior.** Those are point-in-time
  plans; reconciliation lives in `docs/deviations.md`.
- **When you make a change that invalidates a KB file, regenerate it** by running
  `/refresh-kb` (or regenerate the affected file from source and bump its provenance header to
  the new commit). This is a best-effort nudge; provenance headers make staleness detectable
  even when it's missed — a header older than `HEAD` for a touched subsystem means re-verify.

KB files today: `architecture.md` (module map + boundaries), `deployment-flow.md` (the
git→build→run→route pipeline), `conventions.md` (how the code is organized / how to add a
feature).
