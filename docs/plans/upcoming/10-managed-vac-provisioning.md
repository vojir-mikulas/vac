# 10 — Managed VAC: one-click VPS provisioning

**Tier:** Monetization seed · **Effort:** XL · **Status:** stub (furthest out)

## Goal

Automate the whole setup: from a hosted dashboard, "connect your cloud account → pick a
size → 3 minutes later you have a running VAC at `you.vac.app`." Removes the manual get-a-
VPS / SSH-in / run-install.sh / point-DNS / wait-for-certs steps.

## Why it matters (strategy)

This is **Managed VAC** — the strategy's primary realistic monetization path ("own your
infrastructure without operating it"). The biggest revenue swing; the thing the
shared-`vac-db` and "Managed VAC" framing was quietly built toward.

## The load-bearing architectural rule

> The VAC **product** stays a dumb, simple, single-node binary that knows nothing about
> clouds. A **separate hosted orchestrator** creates boxes and installs that binary. They
> never bleed into each other.

This is the *only* place a control plane outside the single box is correct. The strategy's
"no multi-cloud abstraction" applies to the **product**; the managed orchestrator is the
**business**. The moment provider-specific logic leaks into `vac-api`, the moat is violated.

## Two models (both build on the existing `install.sh`)

- **Bring-your-own-cloud (token):** user pastes a provider API token (start with **one**
  provider — Hetzner is the indie/price darling). Orchestrator creates a box, injects a
  cloud-init that runs `install.sh`, registers the instance. User owns the box + bill.
- **Bring-your-own-box (SSH):** user gives IP + SSH key to a fresh Ubuntu box; orchestrator
  SSHes in and runs the same bootstrap. Simpler, no token, works anywhere.

## The stepping stone (do this first)

**"Managed VAC lite": polished `install.sh` + a managed-updates agent.** User runs their
own box; VAC keeps it patched/monitored/backed-up. Validates demand for the managed tier at
a fraction of the build, and every later provisioning model reuses the bootstrap + update
plumbing. (Note: improvements README lists auto-update as a disabled placeholder today.)

## What it requires (why it's last)

- Thin provider adapters (create/destroy/resize — one provider first).
- Cloud-init / SSH bootstrap on `install.sh` (already ~80% there).
- Hosted control surface: accounts, billing, fleet registry.
- **Secure cloud-token handling** — holding credentials that can spin up billed infra is
  the scariest part and a real liability surface.
- Fleet ops: managed updates, health monitoring, cross-box backups.

## Dependencies

- Needs Tier 1–2 rock-solid first (you're selling reliability that must already exist).
- Reuses backups (08) and benefits from managed DBs (09).
