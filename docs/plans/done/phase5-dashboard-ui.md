# VAC Phase 5 — Dashboard UI Plan

> Implements the **Phase 5 — Dashboard UI** milestone from [`mvp.md`](./mvp.md) (L719–729).
> Builds the full React SPA on top of the existing `ui/` scaffold, matching the
> design language in `design/project/VAC Dashboard.html` pixel-for-pixel using
> **shadcn/ui + Tailwind v4**, wired to the Phase 1–4 API and WebSocket surfaces.

---

## 1. Goal & Scope

Deliver a complete, production-quality dashboard SPA that lets a single operator
manage every app, deployment, service, log stream, env var, and setting on their
VAC host — with live build/runtime logs and live per-service stats, dark mode, and
a first-run onboarding wizard.

### In scope (this phase)
- Design-system foundation: tokens, fonts, dark mode, shadcn component install.
- App shell: sidebar (nav + host meters), topbar (breadcrumbs, search, avatar).
- Auth: login + TOTP step, route guard, logout.
- Global dashboard (Apps list = stat strip + apps table + activity + container budget).
- New-App flow (repo connect, key, test connection).
- Per-app detail with tabs: **Overview · Services · Deploys · Logs · Environment · Settings**.
- Global Deployments page, Log Explorer page, Database page, account Settings page.
- Onboarding wizard (UI level, first boot).
- Real-time: WebSocket hooks feeding TanStack Query cache (logs + stats).
- `.env` paste import on the Environment tab.

### Out of scope (later phases / post-MVP)
- Terminal/exec into container (Services tab — post-MVP).
- Managed user databases UI beyond what the design's Database page shows (shared
  Postgres status + backups info only).
- The prototype **Tweaks panel** (accent/font/density live editor) — design-time only.
- Multi-host server switching (sidebar server selector is display-only for MVP).

### Current state (already scaffolded — do not re-create)
`ui/` already has: React 19, Vite 8, TanStack Router (file-based, `autoCodeSplitting`),
TanStack Query, Tailwind v4 via `@tailwindcss/vite`, shadcn (`new-york`, base `neutral`,
`cssVariables`), `lucide-react`, Vitest + RTL, ESLint + Prettier. Build outputs to
`api/internal/ui/dist`, embedded and served by Go with SPA fallback
(`api/internal/ui/handler.go`). Dev server proxies `/api` → `localhost:3000`.
`@` aliases `./src`.

---

## 2. Design-System Foundation (do this first — everything depends on it)

The current `ui/src/index.css` ships the **default** shadcn neutral palette (grayscale
oklch). The design uses a **warm-tinted** palette with an **orange accent**, Geist /
Geist Mono fonts, and a richer semantic color set (`ok` / `warn` / `err`, each with
`fg` / `bg` / `border` variants). Step one is to port `VAC Dashboard.html`'s `:root`
and `[data-theme="dark"]` tokens into `index.css`, mapped onto shadcn's variable names.

### 2.1 Token mapping (`src/index.css`)
Replace the `:root` / `.dark` blocks. Map the design tokens onto shadcn's names so all
shadcn components inherit the palette automatically, and add the extra semantic
families the design needs.

| Design token | shadcn var | Notes |
|---|---|---|
| `--bg` | `--background` | warm near-white / near-black |
| `--fg` | `--foreground` | |
| `--surface-1` | `--card`, `--muted`, `--secondary` | elevated surfaces |
| `--surface-2` | `--accent` (the shadcn "hover/selected" accent, not brand orange) | |
| `--muted-fg` | `--muted-foreground` | |
| `--border` | `--border` | |
| `--border-strong` | `--input`, `--ring` | stronger edges / focus |
| brand orange `#f97316` | **new** `--brand` + `--brand-foreground` | NOT shadcn `--accent` |
| `--ok / --warn / --err` (+ `-fg/-bg/-border`) | **new** semantic families | for StatusPill, charts, meters |
| `--radius: 10px` | `--radius` | set to `0.625rem` (already), confirm = 10px |

> ⚠️ Naming trap: shadcn's `--accent` is the neutral hover/selected surface, **not** the
> brand orange. Keep the orange as a separate `--brand` family so we don't accidentally
> paint every shadcn hover orange. The design's "primary" button is orange → wire shadcn
> `--primary` to `--brand` only if we want all default buttons orange; the design uses
> orange for `variant="primary"` and near-black for `variant="default"`, so map
> shadcn `--primary` → near-black (`--fg`) and expose orange via a `brand` button
> variant (see 4.3).

Register the new families in the `@theme inline` block so they become utilities
(`bg-ok`, `text-err-foreground`, `border-warn-border`, `bg-brand`, etc.):

```css
@theme inline {
  /* …existing… */
  --color-brand: var(--brand);
  --color-brand-foreground: var(--brand-foreground);
  --color-ok: var(--ok);
  --color-ok-foreground: var(--ok-fg);
  --color-ok-bg: var(--ok-bg);
  --color-ok-border: var(--ok-border);
  /* repeat for warn-*, err-* */
}
```

### 2.2 Dark mode
- shadcn convention is `.dark` on `<html>`; the prototype uses `[data-theme="dark"]`.
  Standardize on shadcn's `.dark` class (the `@custom-variant dark` is already wired).
- Theme provider: a tiny `ThemeProvider` + `useTheme()` hook in `src/components/theme/`.
  Read initial value from `localStorage["vac.theme"]`, fall back to
  `prefers-color-scheme`. Toggle adds/removes `.dark` on `document.documentElement`.
- **No-flicker:** inline a blocking script in `index.html` `<head>` that sets the class
  before first paint (rule `rendering-hydration-no-flicker`). Persist with a versioned
  key (rule `client-localstorage-schema`).
- Toggle lives in the topbar (sun/moon `lucide` icon button). Satisfies the success
  criterion "Dark mode toggle works and persists".

### 2.3 Fonts
- Load **Geist** + **Geist Mono** (the design's `--font-sans` / `--font-mono`). Prefer
  self-hosting via `@fontsource/geist-sans` + `@fontsource/geist-mono` (offline, no
  Google Fonts CDN dependency for a self-hosted PaaS). Set `--font-sans` / `--font-mono`
  and register as `--font-*` theme tokens so `font-sans` / `font-mono` utilities resolve.
- Enable `font-feature-settings: "ss01","cv11"` and antialiasing on `body` (per design).

### 2.4 Tailwind sizing convention (per user instruction)
**Never use arbitrary bracket values (`w-[248px]`, `text-[13.5px]`).** Use the standard
scale; for off-scale structural dimensions, **add named tokens to `@theme`** so they
become first-class utilities.

Design px → Tailwind scale (most design values already land on the scale):

| px | utility | | px | utility |
|---|---|---|---|---|
| 4 | `1` | | 16 | `4` |
| 6 | `1.5` | | 18 | `4.5` |
| 8 | `2` | | 20 | `5` |
| 10 | `2.5` | | 24 | `6` |
| 12 | `3` | | 28 | `7` |
| 14 | `3.5` | | 32 | `8` |
| 56 (topbar) | `h-14` | | 248 (sidebar) | **`w-sidebar`** (custom) |

Off-scale structural sizes get **named** theme tokens (still "tailwind-provided", just
custom-named — no brackets):

```css
@theme {
  --spacing-sidebar: 15.5rem;   /* 248px → w-sidebar */
  --container-content: 82.5rem; /* 1320px max content → max-w-content */
}
```

Font sizes: round the prototype's half-pixel sizes to the nearest standard
(`13.5`→`text-sm`, `12.5`→`text-xs`, `11`→`text-xs`/`text-[11px]`-avoid → define
`--text-2xs: 0.6875rem` if 11px fidelity matters). Prefer the standard `text-xs … text-3xl`
ramp; only add `--text-2xs` if reviews show 11px labels look wrong at `text-xs`.

---

## 3. Project Structure

```
ui/src/
  routes/                      # TanStack file-based routes (thin: layout + data wiring)
    __root.tsx                 # providers already here; add devtools
    index.tsx                  # redirect → /apps
    login.tsx                  # login + TOTP step (no shell)
    setup.tsx                  # onboarding wizard (no shell, first-run gate)
    _app.tsx                   # authed layout: <AppShell><Outlet/></AppShell> + beforeLoad guard
    _app/apps.index.tsx        # global dashboard / apps list
    _app/apps.new.tsx          # new-app flow
    _app/apps.$appId.tsx       # app-detail layout (loads app, renders tabs + <Outlet/>)
    _app/apps.$appId.overview.tsx
    _app/apps.$appId.services.tsx
    _app/apps.$appId.deploys.tsx
    _app/apps.$appId.logs.tsx
    _app/apps.$appId.environment.tsx
    _app/apps.$appId.settings.tsx
    _app/deployments.tsx
    _app/logs.tsx              # global log explorer
    _app/database.tsx
    _app/settings.tsx          # account: profile, sessions, 2FA, API tokens, notifications
  components/
    ui/                        # shadcn primitives (generated)
    theme/                     # ThemeProvider, theme-toggle
    layout/                    # AppShell, Sidebar, Topbar, SidebarMeter
    common/                    # StatusPill, StatTile, MetricCard, EmptyState, ConfirmDialog,
                               #   CopyButton, RelativeTime, LogViewer, CodeBlock
  features/                    # screen-specific composition (co-locate with their route)
    apps/ deployments/ services/ deploys/ logs/ env/ settings/ onboarding/ auth/
  lib/
    api/                       # typed fetch client + endpoint fns (one file per resource)
    query/                     # queryKeys factory, queryClient options
    ws/                        # WebSocket hooks (useDeploymentLogs, useAppLogs, useAppStats…)
    format/                    # bytes, duration, relative-time, sha helpers
    utils.ts                   # cn() (exists)
  types/                       # shared TS types mirroring API DTOs (App, Service, Deployment…)
  hooks/                       # cross-cutting hooks (useMediaQuery, useLocalStorage)
```

Principles:
- **Routes stay thin** — they wire loaders/queries and render a feature component.
  All real UI lives in `features/` and `components/`.
- **No components defined inside components** (rule `rerender-no-inline-components`) —
  every `StatTile`, `AppRow`, etc. is a top-level export.
- Direct imports only; **no barrel `index.ts` re-exports** (rule `bundle-barrel-imports`).

---

## 4. shadcn Components & Shared Primitives

### 4.1 shadcn components to install (`npx shadcn@latest add …`)
`button card badge input label textarea select dropdown-menu dialog alert-dialog
sheet tabs table tooltip switch separator skeleton sonner (toast) avatar
scroll-area popover command (⌘K) breadcrumb progress chart`

> `chart` = shadcn Charts (Recharts) for the Overview CPU/Memory line charts.

### 4.2 Shared primitives to build (map prototype → shadcn)
| Prototype element | Implementation |
|---|---|
| `StatusPill` (running/building/crashed/stopped/success/failed) | `Badge` variants + dot span; building state uses `animate-pulse`. Drive colors from the `ok/warn/err` token families. |
| `Button` variants (default/primary/outline/ghost/danger) | shadcn `Button` + a `brand` variant (orange) and `danger` variant via `buttonVariants` cva. |
| `Card`, `vac-card-hover` | shadcn `Card` + `hover:border-border-strong hover:bg-card` transition utility. |
| `StatTile` / stat strip | composed `Card` grid; numbers use `font-mono tabular-nums`. |
| `SidebarMeter` / `BudgetRow` | shadcn `Progress` (or thin div meter) with `>80% → bg-err`. |
| `Topbar` search + ⌘K | shadcn `Command` dialog bound to `⌘K`. |
| `LogViewer` | virtualized list (see §7) with level/service coloring. |

### 4.3 Button variants (cva)
Extend `buttonVariants`: keep shadcn `default/outline/ghost/destructive`, add
`brand` (orange `bg-brand text-brand-foreground`) used for primary CTAs ("New App",
"Deploy from HEAD"). The prototype's near-black "default" maps to shadcn `default`
(since we mapped `--primary` → near-black).

---

## 5. Routing & Navigation

- File-based routes (plugin already enabled). `index.tsx` → `redirect` to `/apps`.
- `_app.tsx` pathless layout route holds the `AppShell` and an auth `beforeLoad` guard
  (see §6). All authed pages nest under it.
- `apps.$appId.tsx` loads the app once and renders the tab strip (shadcn `Tabs` styled
  as the detail nav) + `<Outlet/>`; tab routes are children so deep links / refresh land
  on the right tab (SPA fallback in Go already serves `index.html` for unknown paths).
- Breadcrumbs in the topbar derive from the matched route (`useMatches`), replacing the
  prototype's manual `crumbs` array.
- Sidebar nav items: **Apps · Deployments · Database · Logs · Settings** (matches
  `shell.jsx`). Active state via `Link` `activeProps`.

---

## 6. Auth Flow & Data Layer

### 6.1 API client (`lib/api/`)
- One small `fetchJson` wrapper: base `/api`, `credentials: "include"` (cookie session),
  JSON parse, throws typed `ApiError` on non-2xx. Inject CSRF token header for mutating
  verbs (the API uses CSRF middleware — read token from the cookie/meta per Phase 1 impl).
- One module per resource (`apps.ts`, `deployments.ts`, `services.ts`, `env.ts`,
  `auth.ts`, `sshKeys.ts`, `setup.ts`) exporting plain async fns mapped 1:1 to the
  [API Surface](./mvp.md#L301). Types live in `types/`.

### 6.2 TanStack Query
- `queryKeys` factory in `lib/query/keys.ts` (`apps.all`, `apps.detail(id)`,
  `apps.deployments(id)`, `apps.env(id)`, `auth.me`, …).
- Sensible defaults: `staleTime` per resource, `refetchOnWindowFocus` for live-ish data.
  **Stats/logs come over WS** (§7), not polling — avoid duplicate polling waterfalls.
- Mutations (`useMutation`) for deploy / start / stop / restart / env PUT / settings,
  with `invalidateQueries` + optimistic updates where safe, and `sonner` toasts.

### 6.3 Auth & guard
- `beforeLoad` in `_app.tsx` ensures `auth.me` is loaded; on 401 → `redirect({ to: '/login' })`.
- `/login`: username+password → API returns either full session or **pre-auth** (2FA
  required) → render TOTP code step → `/api/auth/totp`. On success invalidate `auth.me`
  and route to `/apps`.
- Logout button (avatar dropdown) → `POST /api/auth/logout` → clear cache → `/login`.
- First-run: if `GET /api/auth/me`/setup-state indicates no user yet → route to `/setup`
  (onboarding wizard). Replaces the prototype's `localStorage["vac.onboarded"]` gate with
  real server state.

---

## 7. Real-time (WebSocket hooks → Query cache)

Per `mvp.md` Frontend Stack: native WebSocket, messages written **directly into the
Query cache** so components stay reactive without separate state.

Implement in `lib/ws/`:
- `useDeploymentLogs(did)` → `WS /api/deployments/:did/logs` — live build log stream.
- `useAppLogs(appId, { service?, level? })` → `WS /api/apps/:id/logs` — interleaved
  runtime logs tagged by service.
- `useServiceLogs(appId, name)` → `WS /api/apps/:id/services/:name/logs`.
- `useAppStats(appId)` → `WS /api/apps/:id/stats` — per-service CPU/RAM/uptime.

Shared `useWebSocket(url)` primitive:
- Opens on mount, closes on unmount; reconnect with backoff; pause when tab hidden.
- Appends parsed messages via `queryClient.setQueryData(key, draft => …)`, capped to a
  ring buffer (config `logs.ring_buffer_lines`) so memory stays bounded.
- Store the socket + handlers in **refs**, not state, to avoid reconnect-on-render
  (rules `advanced-event-handler-refs`, `rerender-use-ref-transient-values`).
- Stats updates feed both the Services table (live numbers) and the Overview charts.

**LogViewer** component: virtualized (e.g. `@tanstack/react-virtual`) to render
thousands of lines cheaply (rule `rendering-content-visibility` spirit), auto-scroll
toggle, level/service color coding (mono font), and the Export button
(plain text / JSON of the current filtered buffer).

---

## 8. Screen-by-Screen Breakdown

Each screen is recreated **pixel-perfectly** from its prototype file in `design/project/src/`.
Read the named file as source of truth for spacing/colors before building.

| Screen | Route | Prototype ref | Key pieces |
|---|---|---|---|
| **Apps / Global Dashboard** | `/apps` | `view-apps.jsx` | stat strip, filter row + chips, apps `Table`, Activity rail, Container budget card |
| **New App** | `/apps/new` | `view-new-app.jsx` | repo URL, branch, SSH key picker/create, **Test connection** action, framework auto-detect |
| **App Detail shell** | `/apps/$appId` | `view-app-detail.jsx` | detail header (name, repo, domain, status, Deploy CTA), tab nav |
| · Overview | `…/overview` | `view-app-detail-tabs.jsx` | aggregate stat strip, CPU/Mem line charts w/ 1h·6h·24h range + per-service selector, services table, domains+TLS, recent deploys |
| · Services | `…/services` | ″ | per-service cards (status/cpu/mem/uptime/restarts), restart/stop/start with dependency warning, domain+port config |
| · Deploys | `…/deploys` | ″ | "Deploy from HEAD" CTA, history list, expandable step timeline + **live build logs** (WS) |
| · Logs | `…/logs` | ″ | LogViewer, service + level filters, time range, auto-scroll, export |
| · Environment | `…/environment` | ″ | key/value editor, per-var show/hide, **paste .env import**, "Restart required" banner |
| · Settings | `…/settings` | ″ | General, Source (branch/autodeploy + webhook URL), Runtime (RAM/health/restart policy), Danger zone |
| **Deployments (global)** | `/deployments` | `view-deployments.jsx` | metrics (avg build time, today, success rate, in-progress), in-progress step indicator, cross-app timeline |
| **Log Explorer** | `/logs` | (prototype placeholder) | cross-app LogViewer, app/service/time/level filters, tail toggle, export — **TODO: deferred post-MVP. Route stubbed (redirects to `/apps`) and hidden from sidebar/command menu.** |
| **Database** | `/database` | `view-database.jsx` | shared Postgres status, backups info (MVP read-only) |
| **Account Settings** | `/settings` | `view-settings.jsx` | profile, sessions list + revoke, 2FA setup (QR)/disable, API tokens, Notifications (Discord/Slack webhooks) |
| **Onboarding** | `/setup` | `view-onboarding.jsx` | first-boot wizard (create admin, base domain, finish) |
| **Login / TOTP** | `/login` | — (compose from tokens) | password + TOTP step |

Detail-header & tab styling: reuse `vac-detail-head`, `vac-stat-strip`,
`vac-metric-grid`, `vac-settings-row` layout intentions from the prototype's CSS, but
express them as Tailwind `grid`/`flex` utilities with the responsive breakpoints the
prototype encodes (e.g. stat strip 4→2→1 cols).

---

## 9. React Best Practices to Apply

From the project's `vercel-react-best-practices` skill — the high-leverage ones here:
- **Re-renders:** derive state during render not effects (`rerender-derived-state-no-effect`);
  memoize the LogViewer + chart subtrees (`rerender-memo`); functional `setState` for
  stable WS append callbacks (`rerender-functional-setstate`); refs for transient WS
  data (`rerender-use-ref-transient-values`); `useDeferredValue` for the search/filter
  inputs (`rerender-use-deferred-value`); no inline component defs (`rerender-no-inline-components`).
- **Bundle:** direct imports, no barrels (`bundle-barrel-imports`); route-level code
  splitting is on via `autoCodeSplitting`; lazy-load the charts module so Recharts isn't
  in the initial bundle (`bundle-dynamic-imports`).
- **Rendering:** no-flicker theme bootstrap (`rendering-hydration-no-flicker`); ternary
  not `&&` for conditional render (`rendering-conditional-render`); `content-visibility`
  / virtualization for long log lists.
- **Client data:** TanStack Query dedups requests (analogue of `client-swr-dedup`);
  versioned, minimal localStorage (`client-localstorage-schema`); passive scroll
  listeners on the log pane (`client-passive-event-listeners`).
- **Effects:** stable WS handler refs (`advanced-event-handler-refs`,
  `advanced-use-latest`); init-once for the theme/WS singletons (`advanced-init-once`).

---

## 10. Testing

- **Vitest + RTL** (already configured). Co-locate `*.test.tsx`.
- Unit: format helpers (bytes/duration/relativeTime), `StatusPill`/`Badge` mapping,
  `buttonVariants`, the WS ring-buffer reducer (pure fn, test without a socket).
- Component: render each route's feature with a mocked API (MSW or fetch mock) +
  a `QueryClientProvider` + memory router; assert empty/loading/error/success states.
- Smoke: extend the existing `smoke.test.tsx` to mount the shell + a couple of routes.
- Keep `pnpm/npm run typecheck`, `lint`, `format:check` green; wire into CI alongside Go.

---

## 11. Build & Embed Integration (already wired)

- `vite build` → `api/internal/ui/dist` (embedded via `embed_real.go`, served by
  `ui.Handler()` with SPA fallback). No backend change needed.
- Verify the production `vac` binary serves the built SPA and that deep links
  (`/apps/foo/logs`) fall back to `index.html`.
- Add a top-level build step so `make`/CI builds the UI before embedding (Go build tag
  selects `embed_real` vs `embed_stub`).

---

## 12. Milestones (incremental, each independently mergeable)

- [ ] **M1 — Foundation:** port design tokens to `index.css`, dark mode + no-flicker
  bootstrap, fonts, Tailwind theme tokens (`w-sidebar`, semantic colors), install shadcn
  components. *Exit:* a styled button/card/badge render in VAC colors, dark toggle persists.
- [ ] **M2 — Shell + routing:** `AppShell` (Sidebar + Topbar), `_app` guard, route tree,
  `StatusPill`, breadcrumbs, ⌘K command palette skeleton.
- [ ] **M3 — Auth:** API client + Query setup, login + TOTP, logout, first-run → `/setup`.
- [ ] **M4 — Apps dashboard:** stat strip, apps table, activity feed, container budget
  (live host meters in sidebar). *Hits multiple success criteria.*
- [ ] **M5 — App detail (static tabs):** detail header + tab nav; Overview (stats +
  charts), Services, Settings, Environment (incl. `.env` paste import).
- [ ] **M6 — Deploys + real-time:** WS hooks; Deploys tab with live build logs; Logs tab
  + global Log Explorer with live runtime logs; live stats into Services table + charts.
- [ ] **M7 — New App + Deployments + Database + account Settings + Onboarding:**
  remaining screens, notifications config, sessions/2FA/API-token management.
- [ ] **M8 — Polish & verify:** responsive breakpoints, empty/loading/error states,
  a11y pass, tests green, build embeds, manual e2e against a running API.

---

## 13. Risks & Open Questions

- **Token-name collision** (shadcn `--accent` vs brand orange) — resolved in §2.1; double-check
  no shadcn primitive unexpectedly turns orange after the port.
- **CSRF token retrieval** — confirm how the Phase 1 middleware exposes the token to the
  SPA (cookie readable by JS vs meta tag vs `/api/auth/me` payload) before writing the client.
- **WS auth** — confirm WebSocket endpoints accept the session cookie (or need a ticket);
  affects `useWebSocket`.
- **Setup-state detection** — confirm the endpoint/field that says "no admin yet" to gate `/setup`.
- **Pixel half-sizes** (13.5px etc.) — agree up front to round to the standard text ramp
  rather than minting `text-[13.5px]`, per the no-arbitrary-values rule.
- **Database page scope** — confirm MVP is read-only status/backups view (no managed-DB CRUD).
```