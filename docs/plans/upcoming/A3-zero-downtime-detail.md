# A3 — Zero-downtime / rolling deploys · Detailed plan

**Effort:** L · **Status:** design (spike-gated) · **Depends on:** A1 (rollback) + A2
(push-to-deploy) landed.

Goal: a redeploy of a **stateless HTTP service** serves continuously — no 502s — through the
cutover. Bring up the new version alongside the old, let Caddy see it healthy, swap the
route's upstream, drain in-flight requests on the old, then remove it.

This is the hardest item in Track A and the one with a genuine unknown (how to run new+old of
one service under the compose model). So this plan **front-loads a spike** to settle that
mechanism, then specifies everything that is mechanism-independent in full.

---

## 1. The core tension

Today the pipeline (`deploy.Pipeline.Run`) does, per deploy:

```
compose build → compose up -d --remove-orphans → discover containers (ps)
→ attach each to vac-edge as alias {slug}--{service} → Caddy routes to that alias
→ WaitHealthy (poll Caddy /reverse_proxy/upstreams) → running
```

`docker compose up -d` **recreates a changed service's container in place**: it stops the old
container, then starts the new one under the *same* name and the *same* vac-edge alias. Between
"old stopped" and "new healthy + attached" the alias resolves to nothing → Caddy returns 502.
That blip is exactly what A3 removes.

The routing layer is already perfect for blue/green: routing is **by DNS alias on vac-edge**
(`proxy.Manager.dial` → `{slug}--{service}:{port}`), and **Caddy owns active health**
(`routeFor` sets an active health check; `WaitHealthy` polls upstreams). vac-api is off
vac-edge, so cutover is a Caddy admin-API operation, not a container probe. The only missing
piece is **running two generations of one service at once and swapping between them.**

## 2. Invariant: only stateless HTTP services are rollable

You cannot run two generations of a **stateful** service against the same data:

- Two Postgres processes on one volume = corruption. A single-writer DB is fundamentally not
  zero-downtime-rollable.
- Named/bind volumes are the signal: a service with a volume holds state that can't be
  duplicated for an overlap window.

So A3 classifies each service and only rolls the stateless HTTP ones. Everything else is
recreated in place as today (a brief blip on a service that usually has no public route, or is
the DB, is acceptable — and there is no correct alternative for a single-writer store).

### Classification — `rollable(svc)`

A service is **rollable** iff **all** hold:

1. It has an **internal HTTP port** (`services.internal_port != nil`) → it's something Caddy
   routes to. Portless workers/queues aren't user-facing; recreate in place.
2. Its compose definition declares **no `volumes:`** (no named or bind mount) → stateless.
3. It is a **single replica** (no compose `deploy.replicas > 1` / `scale`) → v1 scope.

Detection needs volume info the shallow `compose.Parse` doesn't capture yet. **Extend
`compose.Service`** with `HasVolumes bool` (and optionally `Replicas int`), populated from the
service body's `volumes:` / `deploy.replicas`. The pipeline already parses the resolved compose
file for the build step, so this is one field, not a new pass.

> Edge: a stateless service that *writes to a bind mount for caching* is rare; treating any
> `volumes:` as "don't roll" is the safe default. Document it; revisit if users ask.

## 3. Mechanism — spike this first

Three candidate mechanisms for "new generation alongside old, then swap, then drop old."
**The spike picks one;** the rest of the plan is written to not depend on which.

### M1 — compose `--scale` + image-ID side-by-side *(most compose-native)*
- `compose build` moves the `:latest` tag to the new image; the running old container stays
  pinned to its old image **ID**.
- `compose up -d --no-recreate --scale {svc}=2 {svc}` starts a second container (`{svc}-2`) on
  the **new** image while `{svc}-1` (old image) keeps running.
- Risk: cleanup. Compose numbers containers and scales down highest-first, so telling compose
  "keep the new one, drop the old one" and renormalizing to scale=1 for the next deploy is
  fragile. **The spike must prove a clean renormalization path** (e.g. `docker rm` old + a
  follow-up `up -d` that settles to one container on the new image without a blip).

### M2 — VAC-managed `docker run` generation containers *(cleanest swap/cleanup)*
- compose still owns `build` and all **non-rollable** services (`compose up -d` in place).
- For each rollable service VAC runs the new generation directly:
  `docker run -d --name vac-{slug}-{svc}-{gen} --network {composeNet} <image>` with the env
  file and an `--network vac-edge` attach under a generation alias.
- Swap Caddy, drain, `docker rm -f` the prior generation (tracked by `services.container_id`).
- Risk: re-implements the slice of compose's service→container mapping rollable services need
  (image, env, expose, networks, healthcheck, command). Smaller than it sounds because
  rollable services are stateless and simple (no volumes/depends_on data deps), but it is real
  surface and can drift from compose semantics. The spike measures that surface on a couple of
  real stacks.

### M3 — alternating blue/green project for the whole app *(cleanest model, narrow fit)*
- Active slot per app alternates `vac-{slug}-blue` ⇄ `vac-{slug}-green`; bring the new slot up
  fully, cut over, tear the old slot down.
- Blocked for any app with a stateful service: the new slot would need a second copy of the
  DB/volume. Only viable for **all-stateless** apps. Keep as the special-case fast path if it
  ever proves materially simpler, not the general mechanism.

**Recommendation:** spike **M1 first** (least architectural change, stays inside compose). If
its renormalization is too fragile, fall back to **M2** (VAC owns rollable-service containers).
M3 only if we later decide to special-case all-stateless apps.

### Spike definition (deliverable 0 — do before any production code)
A throwaway script + a tiny stateless HTTP image + a sibling stateful service. Prove on a real
Docker host:
1. New + old of the stateless service run **simultaneously**, both attached to vac-edge under
   distinct aliases.
2. Caddy (driven by the real admin API) routes to old, then swaps to new with **zero failed
   requests** under a `hey`/`wrk` load (continuous 200s across the swap).
3. The old container drains and is removed; the stack **renormalizes** so the *next* deploy
   starts cleanly (no leftover scale gap, no orphan compose state).
4. The sibling **stateful** service is untouched throughout.

Success criteria: **0 non-200s across the cutover**, clean state after, and a documented exact
command sequence. The spike's output is the command sequence that step 6 implements. Record the
chosen mechanism + rationale in `docs/deviations.md`.

## 4. Caddy cutover (mechanism-independent)

- **Generation alias.** During overlap the two containers must have **distinct** vac-edge
  aliases (one alias can't point at two containers deterministically). The new container
  attaches as `{slug}--{service}--{gen}` (gen = short deploy id). The route's upstream dials
  the **current generation's** alias, not the bare stable alias.
- **`Manager.dial` / `routeFor`** read the service's live alias from a new
  `services.route_alias` (or compute `{slug}--{service}--{gen}` from a `services.generation`
  column). Set it on successful cutover.
- **Swap = `PutRoute`** with the new upstream dial. Caddy applies a route replace atomically;
  in-flight requests on the old upstream are unaffected, new requests go to the new alias.
- **Gate before swap** with the existing `WaitHealthy` pointed at the *new* generation's
  upstream (add a transient route or a second upstream on the route, health-check, then narrow
  to the new one). Reuse `caddy.UpstreamStatus` polling — no new health machinery.
- After cutover, optionally also attach the **stable** alias `{slug}--{service}` to the new
  container so non-route consumers still resolve it; routes themselves use generation aliases.

## 5. Draining

Caddy doesn't actively drain on upstream removal — it just stops sending **new** requests once
the route no longer lists the old upstream. So drain = **wait `DrainWindow` after the swap**
before stopping/removing the old container, letting in-flight requests finish. New config
`DrainWindow` (default ~10s), surfaced in the deploy log (`draining old instance (10s)`).
Bound it so a hung deploy can't wait forever (the reaper already covers the outer timeout).

## 6. Pipeline integration

A new branch in `Pipeline.Run`, after build + env render:

```
classify services (rollable vs not)
if first deploy OR no rollable services with a live container:
    normal path (compose up in place, as today)   # nothing to keep alive
else:
    compose up -d non-rollable services            # in place
    for each rollable service with a live old container:
        start new generation (M1/M2 from spike)
        attach new to vac-edge as {slug}--{service}--{gen}
        route includes/points new upstream → WaitHealthy(new)
        on healthy:  PutRoute → swap upstream to new gen
                     log "draining old (Ns)"; sleep DrainWindow
                     remove old generation container; detach old alias
                     update services.container_id + route_alias to new gen
        on unhealthy: DO NOT swap (see §7)
    first deploy of a rollable service: plain up + attach (no old to overlap)
```

Keep the change **inside** `Pipeline.Run`'s existing structure; the non-rolling path is
byte-for-byte today's behavior so portless/stateful/first deploys are unaffected.

## 7. Failure handling — never 502, composes with A1

If the new generation never goes healthy at cutover:

- **Do not swap.** The old container keeps serving; no request ever hits the bad new one.
- Remove the failed new generation container; mark deploy `error` and app `degraded` (mirrors
  today's health-gate-fail handling in `Pipeline.Run`).
- The recovery path is **A1 rollback** — redeploy the last-good commit. A3 is only safe to
  trust *because* A1 exists (the stub's stated dependency).

This preserves the repo invariant: **deploy failure never tears down the running stack.**

## 8. Store / schema

- **`services.route_alias TEXT`** (or `services.generation TEXT`) — the live alias the route
  should dial. Migration **`00022`** (Track A owns `00022+`). `routeFor`/`dial` read it;
  default/empty falls back to the bare `{slug}--{service}` so existing apps and the non-rolling
  path are unchanged.
- Reserve, decide in spike: whether a per-app **active slot** column is needed (only if M3).
- No deployment-row changes needed — generation is a short deploy id already on the row.

## 9. Config

- `DrainWindow time.Duration` (default 10s) — wait after swap before removing old.
- `ZeroDowntime bool` — **global enable** (default on once stable) and/or **per-app override**.
  Recommend a per-app toggle so an operator can opt a finicky app out; store on `apps` or
  instance settings. Decide during implementation; not spike-blocking.
- Health interval/timeout/retries already exist on `proxy.Config`.

## 10. UI

- **Deploy sub-states.** Surface the rolling phases in the deploy timeline. Minimal v1: richer
  system log lines (`starting new instance`, `new instance healthy`, `cutover`, `draining old`,
  `removed old instance`). Optional: a new deployment status (e.g. `cutover`) wired into
  `DeploySteps` — only if the extra granularity earns its keep.
- **Per-app toggle** (if §9 adds one): a "Zero-downtime deploys" switch in Settings → Build or a
  new Deploys-settings area, with copy explaining it applies to stateless HTTP services only.

## 11. Tests

- **Spike** (deliverable 0): scripted, asserts 0 non-200s across cutover (gate before any prod
  code).
- **Unit (pure, no Docker):** `rollable(svc)` classification table; generation-alias
  construction; drain-timing/branch-selection logic; route-upstream swap computation.
- **Integration (testcontainers + Docker):** redeploy a stateless HTTP service under
  continuous load → assert **no non-200** across cutover; a **stateful** service is recreated
  in place, not rolled; a new generation that fails health leaves the old serving and marks the
  deploy `error` (no downtime).

## 12. Sequenced steps

| # | Step | Gate |
|---|---|---|
| 0 | **Spike** the mechanism (§3) → pick M1/M2, record commands + rationale | **blocks all below** |
| 1 | Extend `compose.Parse`/`Service` with volume (+replica) info; `rollable()` classifier + tests | — |
| 2 | Schema `00022` (`services.route_alias`); `routeFor`/`dial` read it; non-rolling path unchanged | — |
| 3 | `proxy.Manager`: route to a named generation alias; atomic upstream swap; `WaitHealthy(newGen)`; old-alias detach | — |
| 4 | Pipeline rolling branch (§6) using the spiked mechanism; non-rollable + first-deploy keep today's path | after 0–3 |
| 5 | Drain window + failure handling (§5, §7); config knobs (§9) | after 4 |
| 6 | UI sub-states (+ per-app toggle if added) | after 4 |
| 7 | Integration tests (§11); `/code-review` + `/simplify`; regenerate `docs/kb/deployment-flow.md`; log mechanism in `docs/deviations.md` | last |

## 13. Open questions / risks

- **Mechanism cleanup (M1)** — can compose renormalize to one-container-on-new-image without a
  blip? This is the spike's central question; if no, fall back to M2.
- **Image retention** — the old generation's image must survive the new build so a mid-cutover
  failure can fall back. This is where per-deployment image tagging + pruner "pin active +
  last-good" finally matters (deferred from A1). Fold into step 4/5.
- **Multi-service cutover ordering** — roll services independently (each its own gate) vs. all
  at once? v1: independent, sequential per service (simpler, smaller blast radius).
- **depends_on across a roll** — a rollable web that `depends_on` a non-rollable db: db is
  brought up first (in place), so the new web generation can reach it. Confirm in the spike.
- **Compose `--remove-orphans`** must not nuke the in-flight new generation (M2 runs rollable
  containers outside the project; ensure the non-rollable `up` doesn't `--remove-orphans` them).
```
