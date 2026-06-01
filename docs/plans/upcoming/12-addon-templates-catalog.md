# 12 — One-click add-on templates (catalog) — Grafana flagship

**Tier:** Monetization seed · **Effort:** catalog M · Grafana template M · **Status:** stub

## Goal

A curated catalog of **one-click app templates / managed add-ons** — pre-configured compose
stacks VAC deploys *as user apps* through the existing pipeline. **Grafana is the flagship**
(observability for VAC + the user's own data), but the same mechanism yields Umami, Plausible,
n8n, Metabase, Uptime-Kuma, etc.

## Why it matters (strategy)

"Remove operational pain / opinionated workflows," and a real monetization surface (a
marketplace, not N bespoke integrations). Reuses everything VAC already does.

## The reframe (do NOT build a bespoke "Grafana feature")

Grafana is just a compose stack, and VAC already deploys arbitrary compose stacks. So:

- A **template** = compose file + default env + a provisioning bundle + a footprint estimate
  + an optional "depends on a managed DB?" flag.
- Deployed through the **existing pipeline** as a normal user app → no control-plane bloat,
  the `<200 MB` control-plane claim is untouched (the add-on runs on the user's box).
- **Guardrail:** templates are data/recipes, never per-add-on code in `vac-api` — same rule
  as build adapters and DB engines.

## Grafana specifics

Three pieces, by difficulty:

1. **"Preselected charts about VAC"** — needs a datasource Grafana can query. Two paths:
   - **Prometheus path** — see plan **13** (expose VAC metrics as Prometheus). Standard but
     heavier (adds a Prometheus process).
   - **Lightweight path (preferred default)** — Grafana reads the `request_metrics` Postgres
     table / VAC's PG directly, **no Prometheus**. Metric volume at MVP scale doesn't need a
     TSDB; roughly halves the footprint.
   - Ship dashboards as **provisioned JSON** baked into the template (Grafana auto-loads
     datasources + dashboards on first boot — zero manual setup).
2. **"Dashboards from managed DBs"** — nearly free once plan **09** exists: point Grafana's
   SQL datasource at the user's managed Postgres/MariaDB → business dashboards off their own
   app data. The "wow" feature. Requires VAC to hand Grafana a (read-only) connection string.
3. **Catalog mechanism** — template registry + an "Add-ons" UI surface. Reusable for every
   later template.

## The honest tension: RAM

Grafana idles ~100–150 MB; +Prometheus adds a few hundred more. On a 2 GB box that's a big
slice. It doesn't break the *control-plane* budget (it's a user app), but:
- Make it **opt-in with a clear footprint warning** ("~300 MB w/ Prometheus, ~150 MB without").
- **Default to the lightweight path** (Grafana → Postgres, skip Prometheus).

## Dependencies

- "Charts about VAC" → plan **13** (or the lightweight PG path).
- "Dashboards from managed DBs" → plan **09**. Sequence this add-on *after* 09.

## Acceptance (sketch)

- A user enables "Grafana" from the catalog → it deploys as a stack, lands pre-provisioned
  with VAC dashboards, and (if managed DBs exist) can query them — with a footprint warning
  shown before install.
