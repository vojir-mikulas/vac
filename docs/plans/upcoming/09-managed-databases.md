# 09 — Managed databases (multi-engine, for user apps)

**Tier:** Monetization seed · **Effort:** L (Postgres path M; +per engine) · **Status:** stub

## Goal

One-click "add a database to your app" — VAC provisions and manages the engine the user
picks (Postgres / MariaDB / MongoDB / Redis), injects the connection string as an env var,
and backs it up (via plan 08). The user *chooses the engine*; VAC handles the rest.

## Why it matters (strategy)

The obvious upsell, and the shared `vac-db` was *deliberately* chosen over SQLite to make
the Postgres path a non-migration (`mvp.md` § Why shared Postgres). Strategy explicitly
lists managed Postgres/Redis as promising paid features. The "user picks the engine" energy
belongs **here** — provisioning recipes for user apps — **not** in VAC's own store (which
stays single-engine; see README "deliberately NOT doing").

## The cost model — one process *per engine*, not per app

This is the load-bearing design decision.

- **Managed Postgres = no new process.** It's a new database + role inside the existing
  `vac-db` instance. Near-free (a few MB of connections). The blessed, cheapest default.
  ```
  vac-db (ONE Postgres process)
    ├── database: vac          ← VAC control-plane data
    ├── database: app_blog     ← managed DB for "blog"
    └── database: app_shop     ← managed DB for "shop"
  ```
- **Any other engine = a separate daemon.** You can't run a Mongo collection or MariaDB
  table inside Postgres. But run **one shared instance *per engine*, multi-tenant by
  database** — NOT a container per app:
  ```
  vac-mariadb (MariaDB)  → app_legacy + app_wp        [only if ≥1 app wants MariaDB]
  vac-redis   (Redis)    → app_blog db0 + app_shop db1 [only if ≥1 app wants Redis]
  ```
  So cost = **(distinct engines in use)** processes, regardless of app count. An all-Postgres
  box has exactly one DB process.
- **Lazy start:** the per-engine instance only spins up the first time an app requests that
  engine. Zero footprint until used.
- **Warn on footprint** at add-time ("starts a shared MariaDB instance, ~150 MB"). Keeps the
  cheap-VPS pitch honest: Postgres is free; another engine costs a known amount.

Rough RAM baselines: Postgres (already paid) ~free per DB · Redis ~5–10 MB · MariaDB
~100–150 MB · Mongo ~100 MB+.

## Provision / deprovision flow

- Create DB + role + password on the engine's shared instance → store encrypted → inject
  the connection string env var (read-only role where it makes sense). Deprovision on app
  delete.
- Reuse the engine-agnostic backup primitive from plan **08** for managed-DB backups.
- Each engine is a small **provisioning recipe** (data/config), never bespoke code in
  `vac-api` — same guardrail as the build adapters.

## Decision to make consciously: shared-with-control-plane vs. isolated

The mvp puts managed user Postgres DBs **on the same instance as VAC's own `vac` database**
(cheapest; fine with per-app roles that can't touch `vac`). The alternative is a **second,
dedicated Postgres instance** for managed user DBs — one extra process bought for blast-radius
isolation between user data and the control plane. Flag in the plan; default to shared, leave
isolated as an opt-in.

## Strategy guardrail

- Do **NOT** start until demand is real — strategy lists managed DBs as a "do NOT initially
  build" item. Build when users ask, not speculatively.

## Open questions

- `max_connections` budget on the shared Postgres (control-plane + user DBs).
- Resource isolation between noisy user queries and VAC's own data (the shared-vs-isolated
  decision above).
- Major-version upgrades of a shared instance with user data on it.
- Redis: shared instance with per-app logical DB index vs. per-app keyspace prefix.

## Acceptance (sketch)

- Adding a managed DB of the chosen engine creates an isolated database (on a shared,
  lazily-started per-engine instance), injects a connection env var, and is covered by
  scheduled backups — with no manual config.
