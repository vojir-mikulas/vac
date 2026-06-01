# 05 — Zero-downtime / rolling deploys

**Tier:** Reliability moat · **Effort:** L · **Status:** stub

## Goal

Eliminate the request blip during a successful redeploy. Bring up the new version, health
check it, swap the Caddy upstream, then drain and remove the old container.

## Why it matters (strategy)

Reliability is the moat. Hard to do well — which is exactly why it's defensible. It's the
natural payoff of the vac-edge / Caddy-health architecture already committed to (D2/D3).

## Rough shape

- The pieces exist: routing is by DNS alias on `vac-edge`, and Caddy already owns active
  health checks.
- Sketch: start the new container alongside the old (distinct alias) → wait for Caddy to
  see it healthy → repoint the route's upstream → drain in-flight requests on the old →
  stop/remove old.
- Compose makes "two versions of one service" awkward — likely needs per-service container
  orchestration outside plain `compose up`, or a blue/green project-name scheme.

## Open questions

- How to run new+old simultaneously within the compose-driven model without breaking the
  "everything is a compose file" invariant.
- Stateful services (db) must NOT be rolled this way — only stateless HTTP services.
- Connection draining window + timeout.

## Dependencies

- Do **after** 01 (push-to-deploy) and 02 (rollback) are solid — rollback is the safety
  net that makes this safe to trust.

## Acceptance (sketch)

- A redeploy of a stateless HTTP service serves continuously (no 502s) through the cutover.
