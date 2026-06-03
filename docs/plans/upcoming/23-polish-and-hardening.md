# 23 — Polish & hardening backlog

**Tier:** Tech-debt / hygiene · **Effort:** S (each item independent) · **Status:** seed (verified against source)

A grab-bag of small, independent cleanups surfaced by a 2026-06-04 codebase sweep. None
is a feature; each is a self-contained hardening or tidy-up that makes the box more honest,
more consistent, or cheaper to run. Land them à la carte — there are no ordering
dependencies between the five items. Sized S each; the whole file is maybe a day.

> These came out of the same sweep that produced the agreed correctness/UX work (page
> error-states, WS connection-state badges, `/health` docker-fork throttle, bcrypt-72
> validation, webhook rate-limit, `internal/admin` tests). Those are tracked separately as
> active work; this file is the **lower-priority remainder** captured so it isn't lost.

---

## 23.1 — Remove (or build) the dead Log Explorer route

**Goal:** kill the dangling global-logs surface, or commit to building it. Right now it's a
ghost.

**Current state (verified):**
- `ui/src/routes/_app/logs.tsx:3-5` is a stub: `// TODO: Log Explorer page (post-MVP)` and
  the component is just `<Navigate to="/apps" replace />`. It references
  `docs/plans/phase5-dashboard-ui.md`, which no longer exists.
- `ui/src/components/layout/topbar.tsx:25` carries a `logs: 'Logs'` label, implying a
  cross-app log surface that doesn't exist. The only real logs UI is the per-app
  `app-detail/logs-tab.tsx`.

**Fix (pick one):**
- **Cheap:** delete `routes/_app/logs.tsx` and the `logs` label/breadcrumb entry, so there's
  no nav path that silently bounces to `/apps`. Regenerate `routeTree.gen.ts` via the normal
  build (don't hand-edit).
- **Bigger:** build a minimal box-wide log explorer (multiplex the existing per-app log WS
  topics, app/service filter, the existing 2000-line ring buffer). Only worth it if an
  operator actually wants cross-app tailing — otherwise prefer the delete.

**Why it matters:** a nav item that redirects to somewhere else teaches the operator the UI
lies. Either make it real or remove it.

---

## 23.2 — Extend the i18n guard to the app chrome

**Goal:** the most-visible chrome (primary nav, app-detail tabs, login/setup) is still
hardcoded English, and the lint guard can't catch regressions there.

**Current state (verified):**
- The `i18next/no-literal-string` guard (`ui/eslint.config.js:52-59`) only covers
  `src/features/**` and `src/components/common/log-*.tsx`. Everything else is exempt.
- Hardcoded, un-migrated: all of `src/routes/**` (incl. app-detail tab labels + the
  "Deploy from HEAD" / "Deploy triggered" strings in `routes/_app/apps/$appId.tsx`),
  `src/components/layout/**` (`sidebar.tsx`, `topbar.tsx`, `command-menu.tsx`), and
  `src/components/auth/**` + `login.tsx` / `setup.tsx`.

**Fix:** migrate the chrome to `t()` (new `chrome`/`nav`/`auth` namespaces under
`src/i18n/locales/en/`), then **widen the eslint `files` glob** to include `src/routes/**`
and `src/components/{layout,auth}/**` so the chrome can't regress. This is a natural
follow-on to the existing i18n work (`docs/plans/ui-i18n.md`), not a duplicate — that plan
deliberately scoped Phase C to feature folders; this closes the remainder.

**Why it matters:** the migration is "done" everywhere except the parts the operator looks
at most, and there's no guard stopping new hardcoded chrome strings from creeping in.

---

## 23.3 — DRY the RemoteAddr → client-IP helpers

**Goal:** one helper for "strip the port off `RemoteAddr`," not five hand-rolled copies that
can drift.

**Current state (verified):** the same `net.SplitHostPort(r.RemoteAddr)` dance is
reimplemented in at least five places, with subtly different fallbacks and return types:
- `api/internal/server/middleware/ratelimit.go:113` — `ratelimitIP` → `string`
- `api/internal/server/middleware/audit.go:158` — `clientIP` → `string`
- `api/internal/server/handler/webhooks.go:254` — `webhookClientIP` → `string`
- `api/internal/server/handler/auth.go:171` — `clientIP` → `*netip.Addr` (different type!)
- `api/internal/server/handler/auth.go:204` — `remoteIPString` → `string`
- (`api/internal/server/handler/sessions.go:128` — `ipString` formats a stored session IP;
  related but not the same concern.)

**Fix:** add one shared helper (e.g. `httpx.ClientIP(r) string` plus a `ClientAddr(r)
(netip.Addr, bool)` for the auth caller that wants a parsed addr), and replace the copies.
Keep the existing documented semantics: trust `RemoteAddr`, ignore `X-Forwarded-For`
(see 23.5) — don't change behaviour, just deduplicate. The shared helper is also the natural
home for the trusted-proxy decision in 23.5, so do 23.3 first if both are taken.

**Why it matters:** five copies of "where does the client IP come from" is exactly the kind
of logic that drifts — and it underpins rate-limiting and audit attribution, where drift is a
security/forensics bug, not a cosmetic one.

---

## 23.4 — Make the DB pool size tunable (and probably smaller)

**Goal:** stop hardcoding 25 max connections for a single-box control plane on a <200 MB
RAM target.

**Current state (verified):** `api/internal/db/db.go:19` — `cfg.MaxConns = 25`, hardcoded,
not env-derived. For one operator and one box, 25 idle-capable Postgres connections is
generous; each costs memory on both the pgx and Postgres sides, working against the idle-RAM
budget that plan 07's benchmark harness defends.

**Fix:** make it env-tunable (e.g. `VAC_DB_MAX_CONNS`, parsed in `internal/config`) with a
**lower default** (8–12 is plenty for the control plane's workload — HTTP handlers + the
deploy worker pool, which itself defaults to 1 concurrent deploy). Log the resolved value at
boot like the other tunables. Sanity-check the new default against the RAM benchmark before
committing the number.

**Why it matters:** it's free idle RAM on a box whose whole pitch is "<200 MB idle," and
"connection count" is a legitimate operator knob on a self-hosted PaaS.

---

## 23.5 — Verify (and wire) the trusted-proxy assumption for client IP

**Goal:** make sure the rate limiter actually sees per-client IPs, not one shared bucket
behind Caddy.

**Current state (verified):**
- `api/internal/server/middleware/ratelimit.go:109-112` documents the assumption verbatim:
  it deliberately ignores `X-Forwarded-For` and trusts `RemoteAddr`, "relying on the trusted
  proxy to set `RemoteAddr` (via Caddy's `trusted_proxies`)."
- `proxy/Caddyfile` is **only a bootstrap** (it just exposes the admin API on `:2019`); the
  real routing config is POSTed by `vac-api` (`api/internal/caddy`). A grep of
  `api/internal/caddy` for `trusted_proxies` / `client_ip_headers` finds **nothing** — the
  generated server config doesn't appear to set them.

**Fix:**
1. **Confirm the topology:** does a control-plane request (dashboard `/api`, `/auth/login`,
   `/webhooks/{appID}`) actually pass *through* Caddy before reaching `vac-api`? (See
   `docs/plans/control-plane-https.md`.) If `vac-api` is hit directly, `RemoteAddr` is
   already the real client and there's nothing to fix — just delete the misleading code
   comment.
2. **If requests do traverse Caddy:** then every client currently shares Caddy's single
   upstream IP, so the auth rate limiter is effectively one global bucket. Set
   `trusted_proxies` (Caddy's docker-network range) + the `client_ip_headers` / forwarded
   handling on the generated server config so Caddy rewrites `RemoteAddr` to the true client
   before proxying, matching the code's stated assumption.

**Why it matters:** if the assumption is wrong, the brute-force protection on
`/auth/login` + `/auth/totp` collapses to a single shared bucket — one slow client can starve
everyone, and per-IP throttling does nothing. It's a security control that may be silently
inert. Worth a 20-minute confirm even if the answer is "we're fine."

---

## Acceptance (per item)

- **23.1** — no nav path redirects to `/apps`; either the route/label are gone or a real
  log explorer renders. `make typecheck` + `make lint` pass; `routeTree.gen.ts` regenerated.
- **23.2** — chrome renders via `t()`; eslint guard covers `src/routes/**` +
  `src/components/{layout,auth}/**`; a hardcoded string in those paths fails `make lint`.
- **23.3** — one shared client-IP helper; the five copies are gone; behaviour unchanged;
  `make test` passes.
- **23.4** — `VAC_DB_MAX_CONNS` honored, lower default logged at boot, RAM benchmark not
  regressed.
- **23.5** — topology documented; either the comment is corrected (direct-hit case) or
  `trusted_proxies` is set and a request behind Caddy buckets by real client IP.
