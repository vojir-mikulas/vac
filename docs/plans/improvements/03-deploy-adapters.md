# 03 — Deploy adapters + new-app walkthrough

**Goal:** Let users deploy more than hand-written compose. The new-app flow lets
the operator choose how the app is built, and VAC auto-detects a sensible default.

This is the largest plan. Backend adapter system + a redesigned multi-step
new-app UI (matching `design/project/src/view-new-app.jsx`).

## Operator decisions (confirmed)

Build-source options to offer:
1. **Static** — serve a built output directory (entry file/dir) via the proxy.
2. **Known framework** — start with **React** only; structured to add a long
   list later (Next.js, Astro, Node, Vite, Python, …).
3. **Single Dockerfile** — with an editable path to the Dockerfile.
4. **Compose** — with an editable path to the compose file (ties to plan 02).

**Auto-detection order** (what VAC pre-selects when scanning the repo):
`compose → Dockerfile → framework → (none: prompt the user to set it up)`.

## Backend: adapter abstraction

**Now:** `api/internal/compose/` only detects/wraps compose & Dockerfile. App
config has `ComposeFile` but no notion of build "kind"
(`store/apps.go`, migration `00004_apps.sql`). The pipeline
(`deploy/pipeline.go`) hardcodes the compose path.

**Change:**
- **Schema:** add to `apps` (new goose migration under `store/migrations/`):
  - `build_kind TEXT NOT NULL DEFAULT 'auto'` — one of
    `auto | compose | dockerfile | framework | static`.
  - `build_config JSONB` — adapter-specific: `{composePath}`,
    `{dockerfilePath}`, `{framework, buildCommand, startCommand, port}`,
    `{staticDir, spaFallback}`.
  - Keep `compose_file` for back-compat / the compose adapter (or migrate it into
    `build_config.composePath` — pick one and note it in `docs/deviations.md`).
- **Adapter interface** in a new `api/internal/adapter/` package:
  ```go
  type Adapter interface {
      Kind() string
      // Prepare resolves/produces a compose file VAC can build & up.
      // Adapters that aren't compose-native synthesize one (template) into repoDir.
      Prepare(ctx, repoDir string, cfg BuildConfig) (composePath string, err error)
  }
  ```
  - `compose`: use the configured/detected path (reuses plan 02's `DetectAt`).
  - `dockerfile`: wrap a given Dockerfile path into a generated compose (extend
    `compose.Wrap` to accept a Dockerfile path + build context).
  - `framework` (React): synthesize a Dockerfile + compose from a template
    (build command → static output served by a tiny static server, or a Node
    runtime image per framework). Keep templates in `adapter/templates/`.
  - `static`: no build image needed for the app itself — serve `staticDir` via
    the proxy (Caddy `file_server`) OR a minimal static-serving container on
    `vac-edge`. Decide which fits the routing model (Caddy owns routing; a tiny
    nginx/caddy file-server container on `vac-edge` keeps the DNS-alias routing
    invariant intact — preferred over special-casing the proxy).
- **Detection:** `adapter.Detect(repoDir) -> kind` implementing the order above;
  used when `build_kind = auto`.
- **Pipeline:** replace the direct `compose.Detect` call (`pipeline.go:178`) with
  adapter resolution: pick adapter by `app.BuildKind` (or detect when `auto`),
  call `Prepare`, then continue the existing build/up/route/health path
  unchanged. The rest of the pipeline stays compose-driven — adapters just
  produce the compose file. This preserves all architecture invariants
  (vac-edge routing, Caddy health-gating).
- **API:** extend create/update (`handler/apps.go`) to accept `build_kind` +
  `build_config`; validate per kind.

**Tests:** per-adapter `Prepare` unit tests (golden compose output);
`adapter.Detect` order tests; integration test deploying a static repo and a
Dockerfile repo.

## UI: new-app walkthrough

**Now:** `ui/src/features/apps/new-app.tsx` is a 2-step flow (Create → Connect)
with a single "compose file" text field.

**Change:** Rebuild as a stepper matching the design
(`design/project/src/view-new-app.jsx`): **Source → Build → Domain → Deploy**.
- **Source:** repo URL (keep SSH/HTTPS detection + deploy-key card, fixed by
  plan 01.7), branch.
- **Build:** the build-source picker — Static / Framework (React; grid of
  options, others shown "coming soon" disabled) / Dockerfile (path input) /
  Compose (path input). Pre-select VAC's auto-detected kind with a "detected"
  badge; allow override. Framework choice reveals build/start command + port
  fields (design rows 131–141).
- **Domain:** custom domain input + DNS hint, or VAC subdomain (ties to plan 06).
- **Deploy:** review summary (`ReviewLine` style) + deploy.
- Persist selections to the new `build_kind` / `build_config` API fields.
- Editing later: surface the same build-source controls in the app **Settings**
  tab (`ui/src/features/app-detail/` settings tab) so it's changeable post-create.

**Accept:** Operator can create a React app, a static site, a Dockerfile app, and
a compose app through the wizard; VAC pre-selects the right kind; each deploys.

## Phasing (recommended)
1. Backend adapter package + schema + pipeline integration, with `compose` and
   `dockerfile` adapters (these mostly formalize existing behavior + plan 02).
2. New-app stepper UI wired to the new fields (compose/dockerfile working).
3. `static` adapter (+ proxy/file-server decision).
4. `framework` adapter with React template; scaffold the list for future frameworks.

## Open design notes (resolve during impl, not blocking the plan)
- Static serving container vs Caddy `file_server` — recommended: tiny
  file-server container on `vac-edge` to keep the routing invariant.
- Framework template strategy: per-framework Dockerfile templates vs a generic
  buildpack-style wrapper. Start with an explicit React template.
