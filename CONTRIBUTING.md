# Contributing to VAC

Thanks for your interest in VAC! This is a young project and contributions — bug reports,
ideas, docs, and code — are all welcome.

## Before you start

- For anything non-trivial, **open an issue first** to discuss the approach. It saves everyone a
  round-trip and avoids work that doesn't fit the project's direction.
- VAC has a deliberate scope: a self-hosted PaaS for a **single VPS, one operator**, with a tight
  **< 200 MB RAM idle** footprint. Features that pull it toward multi-node/Kubernetes territory or
  bloat the runtime are probably out of scope — ask first.
- Read [`CLAUDE.md`](CLAUDE.md) and [`docs/kb/`](docs/kb/) for the architecture and the non-obvious
  invariants (e.g. the control plane stays off the app network; Caddy owns deploy health). Don't
  "fix" those without understanding the trade-off — the reasoning lives in
  [`docs/deviations.md`](docs/deviations.md).

## Development setup

Prerequisites: **Go 1.25+, Node 22+, pnpm 10+, Docker.**

```sh
pnpm install            # root dev hooks
pnpm --dir ui install   # UI deps
make dev                # Go API + Vite dev server
```

See the [README](README.md#development) and the [`Makefile`](Makefile) for all targets.

## Before you open a pull request

Please make sure the following pass:

```sh
make lint               # golangci-lint + eslint + prettier
make typecheck          # tsc
make test               # Go race + vitest unit tests
```

Integration tests (`make test-integration`) need Docker and are slower; run them when your change
touches the deploy pipeline, store, or proxy.

## Coding conventions

- **Backend**: single-responsibility packages under `api/internal/`. HTTP handlers in
  `api/internal/server/handler/`, persistence in `api/internal/store/` (pgx + goose migrations).
  Tests sit next to the code (`*_test.go`); integration tests are tagged `integration`.
- **UI**: feature-foldered under `ui/src/features/`; shared client/WS/query setup in `ui/src/lib/`.
  Routing is file-based via TanStack Router — `routeTree.gen.ts` is generated, don't hand-edit it.
- Match the style of the surrounding code.

## Commits and pull requests

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) and are
  enforced by commitlint (see [`commitlint.config.js`](commitlint.config.js)).
  Example: `fix(deploy): gate rollout on Caddy upstream health`.
- Keep PRs focused; describe the change and the reasoning. Link the issue it addresses.
- If your change invalidates a `docs/kb/` file, regenerate it (see the KB section in `CLAUDE.md`).

## Reporting bugs

Open an issue with steps to reproduce, what you expected, what happened, and your environment
(OS, Docker version, VAC version). For **security** issues, follow [`SECURITY.md`](SECURITY.md)
instead — do not open a public issue.

## License

By contributing, you agree that your contributions are licensed under the
[Apache 2.0 License](LICENSE).
