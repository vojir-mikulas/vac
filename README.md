# VAC

A self-hosted PaaS for a single VPS — connect a Git repo, deploy it as a Docker Compose stack with automatic HTTPS, observe it via a real-time dashboard.

See [`docs/plans/mvp.md`](docs/plans/mvp.md) for the full MVP plan.

## Repository layout

```
api/    — Go backend (vac-api, also worker for MVP)
ui/     — React + TypeScript + Vite SPA (embedded into the Go binary via go:embed)
docs/   — design docs and plans
```

## Development

Prerequisites: Go 1.24+, Node 22+, pnpm 10+, Docker.

```
pnpm install            # root deps (hooks)
pnpm --dir ui install   # UI deps
make dev                # runs Go API + Vite dev server with proxy
```

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
