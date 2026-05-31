# 07 — Mock backend + deployable UI preview

**Goal:** Run the entire UI with **no backend** — a self-contained build that fakes
the API/WebSocket layer with believable, stateful demo data. Deploy it as a static
site (per-PR preview URLs) so the app can be shown and click-tested without a VPS,
Docker, or Postgres.

This is purely additive: a new mock layer + a build flag. It does **not** touch
feature components and does **not** disturb the production path (UI embedded in the
Go binary via `go:embed` + the `embedui` tag).

## Why the UI is well-suited for this

The UI talks to the backend through exactly **two seams**, both centralized:

- **HTTP** — everything funnels through `request()` / the `api` object in
  `ui/src/lib/api/client.ts`. No feature code calls `fetch` directly.
- **WebSocket** — everything goes through `useWebSocket` in
  `ui/src/lib/ws/use-websocket.ts` (plus `ws/use-stats.ts`, `ws/use-log-stream.ts`).

TanStack Query sits on top, so components consume hooks (`useApps()`, …) and don't
care where data comes from. Faking the network makes the whole UI work unchanged.

## Approach: MSW + an in-memory state store

Use **[MSW](https://mswjs.io)** (Mock Service Worker) to intercept `fetch` and
WebSocket at the browser network layer. Preferred over swapping the `api` object
because the **real** `client.ts`, auth, CSRF, error handling, and query/retry logic
stay in the code path — the demo exercises the actual UI, just against a fake server.

- **Build flag:** gate everything behind `VITE_MOCK` (env). In `ui/src/main.tsx`,
  `if (import.meta.env.VITE_MOCK) { await worker.start() }` **before** rendering.
  When the flag is unset, MSW is never imported (tree-shaken out of the real bundle).
- **Isolation:** all mock code lives under `ui/src/mocks/` and is the only place
  importing MSW. Nothing under `features/` changes.
- **Handlers** mirror the route surface used by `ui/src/lib/api/*`:
  - `apps.ts` → `GET/POST /api/apps`, `GET/PATCH/DELETE /api/apps/:id`,
    `POST /api/apps/:id/{start,stop,restart,test-connection,ssh-key/regenerate}`,
    `GET /api/apps/:id/ssh-key`
  - plus `deployments.ts`, `env.ts`, `services.ts`, `domains.ts`, `metrics.ts`,
    `notifications.ts`, `auth.ts`, `setup.ts`, `instance.ts` (one handler file per
    `ui/src/lib/api/` module).
- **In-memory store** backs the handlers (module-level object, seeded from fixtures,
  optionally persisted to `localStorage`). This is what makes it feel alive: create
  an app → it appears in the list; click deploy → status transitions on a timer.

**Contract safety:** handlers and fixtures import the response types from
`ui/src/types/api.ts`, so a backend contract change breaks `make typecheck` on the
mocks too. This is the main guard against silent mock drift — keep it strict.

## The two parts that need real design (not just plumbing)

1. **WebSockets** — live logs, CPU/RAM stats, and deploy-step streaming all push
   `WsFrame` messages (`ui/src/types/api.ts`). Either use MSW's WebSocket support
   or a tiny mock `WebSocket` shim under `ui/src/mocks/`. Provide generators that
   emit `WsFrame`-shaped frames on an interval: fake log lines, a smooth-ish CPU/RAM
   curve (`ws/use-stats.ts`), and deploy-step progress that drives
   `features/app-detail/deploy-steps.tsx` + `live-deploy-banner.tsx`. This is where
   the "demo magic" payoff is highest and the most custom code lives.

2. **Auth / setup / login** — `routes/login.tsx`, `routes/setup.tsx`, sessions,
   TOTP, and the `vac_csrf` cookie. For a preview, **auto-authenticate**: the mock
   session endpoint always returns a logged-in user so it lands on the dashboard.
   Keep a scripted login that accepts anything (so the login screen is still
   demoable). Mark setup as already-completed.

## Stateful behaviors to script (the believable bits)

- **Deploy lifecycle:** `POST` a deploy → the deployment record walks
  `queued → cloning → building → running` (or a scripted `error`/`degraded` to demo
  failure states), with deploy-step frames and log frames streamed in step. Mirror
  the real states in `ui/src/lib/deploy-status.ts`.
- **Stack control:** start/stop/restart flip app + service status realistically.
- **Stats:** continuous host + per-app CPU/RAM stream so meters and the traffic
  chart (`features/app-detail/traffic-chart.tsx`) animate.
- **Env editor:** plaintext vs sensitive keys, reveal, `.env` import (plan 04 surface).
- **Seed fixtures:** 3–4 apps in varied states (running, building, degraded,
  stopped) so the dashboard looks populated on first load.

## Deployment

It's a plain Vite SPA — a mock build is just static files, no server:

```
VITE_MOCK=1 pnpm build      # → static assets (do NOT use the embedui Go path)
```

Host free on Cloudflare Pages / Netlify / Vercel / GitHub Pages; all give per-PR
preview URLs. One gotcha: TanStack Router needs **SPA fallback** (rewrite all paths
→ `index.html`) — one line of host config (`_redirects` / `netlify.toml` /
`vercel.json` / a Pages rewrite). Add a `make preview-build` (or pnpm script) target
so the mock build is reproducible.

## Phasing (recommended)

1. **Plumbing:** add MSW, the `VITE_MOCK` flag + `main.tsx` gate, `ui/src/mocks/`
   skeleton, and the in-memory store. Stub `GET /api/apps` + auth so the dashboard
   loads logged-in. (~half a day)
2. **Core REST flows:** apps list/detail, deployments, env, services, domains —
   stateful CRUD against the store. (1–2 days)
3. **WebSocket simulators:** logs, stats, deploy-step progression to a polished
   level. (~1 day)
4. **Deploy + host config:** SPA-fallback config for one host, a `preview-build`
   target, and a CI preview deploy (optional). (~half a day)

Estimated **2–4 days** total, front-loaded toward making fake data feel real.

## Acceptance criteria

- `VITE_MOCK=1 pnpm build` produces a static bundle that runs with no network
  backend; a normal `pnpm build` contains **zero** MSW/mock code.
- The deployed preview lands logged-in on a populated dashboard.
- A user can: open an app, trigger a deploy and watch it progress through states
  with live logs + steps, start/stop a stack, edit env vars, and see CPU/RAM meters
  animate — all without a backend.
- `make typecheck` covers the mock handlers (they import `ui/src/types/api.ts`), so
  a contract change fails the build rather than drifting silently.

## Open notes (resolve during impl, not blocking)

- MSW native WebSocket support vs a custom mock `WebSocket` shim — pick whichever
  reproduces `WsFrame` streaming with least friction.
- Whether to persist the in-memory store to `localStorage` (nice for demos that
  survive refresh) or reset on every load (cleaner for screenshots/QA). Default:
  reset on load, with a `?persist` opt-in.
- Host choice (Cloudflare Pages vs Netlify vs Vercel) — driven by where preview URLs
  are most convenient for the team.
