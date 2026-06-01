# Upcoming — future direction (stubs)

Brief, expand-later stubs captured 2026-06-01 from a direction review (see
`docs/reviews/2026-06-01-direction-vs-mvp.md`). The MVP is functionally complete;
these are the next horizons. Each file is a *seed* — goal, why-it-matters (tied to
the Strategic Direction), rough shape, effort — not a finished plan. Flesh them out
before building.

Guiding lens (from the strategy): the moat is **simplicity + UX + reliability +
trust**, not feature count. Order by "does this make the deploy loop feel more
trustworthy and effortless," not by what's technically interesting.

> **Executing in parallel?** See [`00-parallel-tracks.md`](00-parallel-tracks.md) — it groups
> these 13 stubs into concurrent tracks by subsystem ownership (Deploy Core / Observability /
> Trust / Managed Services), with the sequencing and collision points worked out.

## Plans

> **Shipped** (moved to [`../done/`](../done/)): **01 push-to-deploy** and **02 rollback** are
> implemented (Track A1/A2). **05 zero-downtime** is **deferred** — detailed design captured in
> [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md), to be evaluated later.
>
> **Track C shipped** (Trust & Lifecycle): **11 audit + revert**, **03 cert-expiry**, and
> **04 onboarding** are implemented. Audit log is exposed as an Activity feed with curated revert
> (`internal/revert`); cert-expiry runs via `internal/certcheck` (resolves deviation D7); onboarding
> is a dismissible first-run checklist on the apps dashboard.

| # | File | Tier | Scope | Effort |
|---|------|------|-------|--------|
| 01 | ✅ [../done/01-push-to-deploy.md](../done/01-push-to-deploy.md) | Close the loop | Git webhook auto-deploy + trigger model (branch / tag / manual) | L |
| 02 | ✅ [../done/02-rollback.md](../done/02-rollback.md) | Close the loop | One-click redeploy of a previous deployment | S–M |
| 03 | [03-cert-expiry-notification.md](03-cert-expiry-notification.md) | Close the loop | Finish deferred D7 notification | S |
| 04 | [04-onboarding-wizard.md](04-onboarding-wizard.md) | Close the loop | Guided connect-repo → first-deploy flow | M |
| 05 | [05-zero-downtime-deploys.md](05-zero-downtime-deploys.md) · ⏸ deferred, [detailed design](A3-zero-downtime-detail.md) | Reliability moat | Rolling deploy: up new → health → swap Caddy upstream → drain old | L |
| 06 | [06-resource-guardrails.md](06-resource-guardrails.md) | Reliability moat | Per-app RAM limits + box-level budget UI + OOM protection | M |
| 07 | [07-ram-benchmark-harness.md](07-ram-benchmark-harness.md) | Reliability moat | Repeatable, CI-enforced idle-RAM measurement | S–M |
| 08 | [08-managed-backups.md](08-managed-backups.md) | Monetization seed | User-defined backup commands → schedule → S3/B2 | M |
| 09 | [09-managed-databases.md](09-managed-databases.md) | Monetization seed | Multi-engine managed DBs (PG/MariaDB/Mongo/Redis), one process per engine | L |
| 10 | [10-managed-vac-provisioning.md](10-managed-vac-provisioning.md) | Monetization seed | One-click VPS provisioning (Managed VAC) + managed-updates stepping stone | XL |
| 11 | [11-audit-log-and-revert.md](11-audit-log-and-revert.md) | Close the loop / moat | Audit log (who did what) + curated revert of safely-invertible actions | M |
| 12 | [12-addon-templates-catalog.md](12-addon-templates-catalog.md) | Monetization seed | One-click add-on templates catalog; Grafana flagship | M |
| 13 | [13-prometheus-metrics-exposition.md](13-prometheus-metrics-exposition.md) | Reliability / observability | Expose VAC metrics on a Prometheus `/metrics` endpoint | S–M |
| 16 | [16-compose-preflight-validation.md](16-compose-preflight-validation.md) | Trust & UX | Preflight lint of user compose: hard-error/warn on edge-port/bundled-proxy/docker.sock/host-ports | M |

## Suggested order

1. **01 push-to-deploy** — highest leverage; turns VAC from a tool into a platform.
2. **02 rollback** — nearly free given the data model; the safety net that makes
   aggressive deploys (incl. 05) emotionally safe. Do alongside 01.
3. **11 audit log** (Part 1) — cheap, attribution is ~free; foundation for revert and a
   trust signal on its own. Its revert half (Part 2) layers on after 02.
4. **03 cert-expiry** + **07 ram-benchmark** — cheap, finish-the-promise items.
5. **04 onboarding** — first-run trust.
6. **06 resource guardrails** — the small-VPS reliability story.
7. **05 zero-downtime** — hardest; do once 01/02 are solid.
8. **13 prometheus exposition** — standalone-useful; unblocks 12's "charts about VAC."
9. **08 → 09 → 12 → 10** — the Managed VAC arc, furthest out (backups → managed DBs →
   add-on catalog/Grafana → full provisioning).

## Deliberately NOT doing (guard the moat)

- No buildpack / framework-coverage arms race — keep build adapters small.
- No multi-node, no teams/RBAC yet — single operator, single box.
- No preview environments yet — Tier-1-distracting complexity for the solo-dev target.
- No multi-cloud abstraction *in the product* — provider logic only ever lives in
  the separate Managed VAC orchestrator (see 10), never in `vac-api`.
- **VAC's own control-plane store stays single-engine** (Postgres; a SQLite-only
  ultra-light mode is the *only* defensible alternative, behind a build tag). Do NOT make
  the internal store pluggable across MariaDB/Mongo — that doubles the persistence
  maintenance surface forever for zero end-user benefit. "User picks the engine" belongs in
  managed DBs *for user apps* (09), not in VAC's own persistence.
