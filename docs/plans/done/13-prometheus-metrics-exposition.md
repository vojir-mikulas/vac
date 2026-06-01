# 13 — Prometheus metrics exposition

**Tier:** Reliability moat / observability · **Effort:** S–M · **Status:** stub

## Goal

Expose VAC's own metrics on a Prometheus-compatible `/metrics` endpoint on `vac-api`: host
CPU/RAM/disk, per-app stats, deploy counts/durations/success-rate, request rates.

## Why it matters (strategy)

Independently valuable beyond Grafana: lets any advanced operator scrape VAC with their own
Prometheus/monitoring, Grafana or not. It's the standard, expected observability seam — and
it's the prerequisite for the "preselected charts about VAC" in plan **12**.

## Context (current state)

- Stats are **live-only / not persisted** (deviation D6) — fine for live gauges, but nothing
  for a scraper to read historically.
- Host stats come from gopsutil; request metrics live in a rolling 24h Postgres window;
  Caddy's `/metrics` is already scraped for host-level aggregate.
- So the new work is a **read-model exposition**, not new data collection — surface what VAC
  already knows in Prometheus text format.

## Rough shape

- Add a `/metrics` handler (guard/auth as appropriate — it can leak topology).
- Export gauges/counters: `vac_host_cpu`, `vac_host_mem`, `vac_app_cpu{app,service}`,
  `vac_deploys_total{status}`, `vac_deploy_duration_seconds`, `vac_requests_total{app}`, …
- Reuse the existing stats/reqmetrics sources; no new collectors needed.

## Open questions

- Auth model for `/metrics` (internal-only vs. token-gated vs. public-but-bland).
- Naming/label conventions (lock them early — dashboards in plan 12 depend on them, and
  renames break user scrapers).
- Whether to also persist a longer history, or leave history to whoever scrapes.

## Dependencies / relationships

- Unblocks the Prometheus path of plan **12** (Grafana "charts about VAC"). Note plan 12's
  *lightweight* path (Grafana → Postgres directly) does **not** require this — so 13 is
  optional-but-nice for 12, and standalone-valuable on its own.

## Acceptance (sketch)

- `GET /metrics` returns valid Prometheus exposition covering host + per-app + deploy +
  request metrics; an external Prometheus can scrape it with stable metric names.
