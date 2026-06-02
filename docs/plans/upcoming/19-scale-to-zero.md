# 19 — Scale-to-zero (sleep idle containers, cold-start on first request)

**Tier:** Reliability moat / density · **Effort:** M–L · **Status:** stub

## Goal

Opt-in, per-app: stop a container after a configurable idle window, and transparently
**cold-start it on the first incoming request** (holding that request until the app is
healthy). Reclaims RAM/CPU from idle apps so a single small VPS can host more of them —
directly serving VAC's "<200 MB idle, one box, many apps" thesis.

Default **off**. Only eligible for **stateless HTTP services** (see eligibility below).

## Why it matters (strategy)

Density is the whole pitch — "cram apps onto one cheap VPS." Idle apps are dead weight on
the RAM budget; sleeping them is the most direct lever to fit more apps per box. And it
reuses VAC's two existing strengths rather than inventing subsystems:

- **Caddy already owns deploy health.** The `→ running` gate polls Caddy's
  `/reverse_proxy/upstreams` admin endpoint. Cold-start "wait until healthy" is the *same*
  poll, just triggered by a request instead of a deploy.
- **Traffic signals likely already exist.** The traffic-anomaly work (Track E / security
  dashboard) computes per-app request counters at the Caddy edge — idle detection wants
  exactly that signal, "no requests for N minutes."

## The architectural hinge — who intercepts the first request

A stopped container is a **dead upstream**, and `vac-api` is deliberately **not on
`vac-edge`**, so it can't sit in the request path to wake things. The interceptor must live
at the **Caddy edge**:

1. Request arrives for `{slug}--{service}`; upstream is down (container stopped).
2. Caddy routes the miss to a small **wake handler** instead of erroring.
3. Wake handler calls `vac-api` to **start the container**.
4. It polls `/reverse_proxy/upstreams` until the upstream is healthy (reuse deploy gating).
5. It **holds / retries** the original request through to the now-live upstream; past a
   timeout it serves a lightweight **"starting…" holding page** (auto-refresh).

**Decision: build a custom in-house wake handler, not the [Sablier](https://github.com/acouvreur/sablier)
Caddy plugin.** Sablier ships a plugin that does roughly this, but for VAC the custom route wins:

- **No custom Caddy build.** A third-party plugin means an `xcaddy` build pipeline and pinning
  the plugin to our Caddy version forever. VAC ships its own `proxy/` Caddy container with a
  tight dependency surface — keep it stock.
- **Reuse our own health gate.** We already poll `/reverse_proxy/upstreams`; the plugin would
  duplicate that logic out-of-process instead of sharing our deploy-gating code.
- **Tighter state integration.** The wake path needs to drive vac-api's app state model
  (`sleeping`), per-app settings, the stampede start-lock, and notifications — all of which
  live in `vac-api`. A custom handler talks to those directly; the plugin would need a
  side-channel.
- **No external release-cadence coupling.** We own the upgrade/Caddy-compat story.

Shape of the custom handler: Caddy routes the upstream-miss for a `sleeping` service to a
small wake endpoint (in `vac-api`, or a tiny sidecar on `vac-edge` that proxies to it). The
wake trigger flows Caddy → vac-api (start container) — the inverse of the normal "vac-api
can't reach app containers" rule, which is fine because it's a control action, not app
traffic. (Sablier stays the reference implementation to crib request-holding behavior from.)

## Eligibility — flag stateful, never sleep it

Sleeping breaks anything doing work between requests. Gate on it:

- **Eligible:** stateless HTTP services only — web frontends, request/response APIs.
- **Flagged stateful → never slept** (the user's preferred framing): apps the operator
  marks stateful, **plus auto-detected red flags**:
  - published **host ports** (not pure `vac-edge` HTTP routing),
  - background workers / cron / queue consumers (no HTTP route, or declared as such),
  - long-lived **WebSocket/SSE** connections (also confuse idle detection — a held-open
    connection looks like activity, or looks idle while still open).
- The compose-preflight pass (16) is the natural place to surface "this service isn't
  scale-to-zero eligible because …".

## Trade-offs to surface in the UI (don't hide them)

- **First-request latency**: Docker start + app boot, often several seconds; the first user
  after idle eats it. Fine for hobby/staging, a real choice for latency-sensitive apps —
  make the holding page honest about it.
- **Idle window** is a per-app tunable; too aggressive = constant cold starts on low-traffic
  apps. Sensible default (e.g. 15–30 min), operator-overridable.
- **Wake storms / stampede**: concurrent first requests must trigger **one** start, not N —
  the wake handler needs a per-service start lock.

## Rough shape

- **Per-app setting**: `scale_to_zero` (off by default) + `idle_timeout`, with eligibility
  guard (refuse to enable for flagged-stateful / ineligible services).
- **Idle reaper** in `vac-api`: consume the per-app request counters; when an eligible app
  has zero traffic for `idle_timeout`, `docker stop` it and mark state `sleeping`.
- **Wake path** at the Caddy edge: custom handler → vac-api start endpoint → reuse
  upstream-health gating → release held request / holding page.
- **State + UI**: a `sleeping` app state distinct from `stopped`/`error`, shown on the
  dashboard; surface "slept N apps, reclaimed ~X MB" as a density win.

## Open questions

- Where the wake handler runs: a route on `vac-api` reached via a tiny `vac-edge` shim, vs. a
  minimal sidecar on `vac-edge`. Whichever keeps the Caddy → vac-api start call simplest to
  authenticate without putting `vac-api` itself on `vac-edge`.
- How the wake-start control endpoint is authenticated (Caddy → vac-api) and how Caddy is
  configured to route an upstream-miss for a `sleeping` service to it (handle/route ordering).
- Request-holding mechanics — hold the connection and retry internally vs. holding page +
  auto-refresh. (Sablier is the reference for the held-connection approach.)
- How idle detection coexists with WebSocket/SSE long-lived connections (probably: any such
  service is ineligible).
- Interaction with **resource guardrails (06)** and **zero-downtime deploys (05)** — a
  sleeping app shouldn't count against the RAM budget; a deploy of a sleeping app should
  leave it sleeping (or wake → deploy → re-sleep?).

## Acceptance (sketch)

- An operator enables scale-to-zero on an eligible stateless HTTP app and sets an idle
  window. After the window with no traffic, the container stops and the app shows `sleeping`;
  box RAM drops. The next request transparently starts it (holding page if slow), serves the
  response once healthy, and a stampede of concurrent first requests triggers a single start.
  Flagged-stateful / port-publishing / worker services cannot enable the feature and are
  never slept.
