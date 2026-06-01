# 09 — Managed databases (Postgres / Redis)

**Tier:** Monetization seed · **Effort:** L · **Status:** stub

## Goal

One-click "add a database to your app" — VAC provisions and manages a Postgres (and later
Redis) instance, injects the connection string as an env var.

## Why it matters (strategy)

The obvious upsell, and the shared `vac-db` was *deliberately* chosen over SQLite to make
this a non-migration later (`mvp.md` § Why shared Postgres). Strategy explicitly lists
managed Postgres/Redis as a promising paid feature.

## Rough shape

- The mvp's model: each managed DB is a separate **database on the shared `vac-db`
  instance** (`app_myapp`, `app_blog`, …), not a new container per app — keeps RAM low.
- Provision: create DB + role + password → store encrypted → inject `DATABASE_URL` into the
  app's env. Deprovision on app delete.
- Redis likely needs its own lightweight container (different model than the shared PG).

## Strategy guardrail

- Do **NOT** start until demand is real — the strategy says managed DBs are explicitly a
  "do NOT initially build" item; build it when users ask, not speculatively.
- Reuse the backups primitive (plan 08) for managed-DB backups.

## Open questions

- Connection limits / `max_connections` budget on the shared instance vs. per-app DBs.
- Resource isolation (one noisy app's queries vs. VAC's own data on the same instance).
- Major-version upgrades of the shared instance with user data on it.

## Acceptance (sketch)

- Adding a managed Postgres to an app creates an isolated database and the app connects via
  an injected env var with no manual config.
