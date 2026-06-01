# 00 — Parallel execution tracks

How to run the 13 upcoming stubs as **concurrent tracks**. The grouping rule is *subsystem
ownership*: each track owns a distinct slice of the codebase so two tracks rarely touch the
same files. Items **within** a track are sequenced (they share files or build on each other);
items **across** tracks run in parallel.

The one hard constraint that drives everything: **the deploy pipeline (`api/internal/deploy`,
`caddy`, `proxy`) is a single hot path.** Push-to-deploy, rollback, and zero-downtime all
rewrite it — so they must be *one* track done in order, not parallel work, or you get merge
hell. Everything else is arranged to stay out of that path.

```
        ┌─ Track A: DEPLOY CORE ──────────── ✅02 → ✅01 → ⏸05    (critical path; 05 deferred)
        │
 land   ├─ Track B: OBSERVABILITY & LIMITS ─ ✅07 → ✅13 → ✅06   (done)
 first  │
 (S0) ──┼─ Track C: TRUST & LIFECYCLE ─────── ✅11 → ✅03 → ✅04  (done)
        │
        ├─ Track D: MANAGED SERVICES ───────── 08 → 09 → 12       (greenfield)
        │
        └─ Track E: MANAGED VAC (separate repo) ─ 10              (future, out of tree)
```

> **Status (2026-06-01):** Stage 0 landed. **Track A** shipped A1+A2 (rollback +
> push-to-deploy); **A3 zero-downtime is deferred/paused** (design in
> [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md)). **Track B** and **Track C** are
> complete. Remaining: A3 (deferred), Track D (greenfield), Track E (separate repo).

---

## Stage 0 — land first (shared seams, ~1–2 days) ✅ *done*

Two small foundations that, if built up front, let the tracks proceed without colliding:

1. **Audit middleware seam** (from `11`, Part 1). Build the `audit_log` table + a *central
   middleware* that records actor/route/outcome for every mutating request. Doing this once,
   centrally, means tracks A–D inherit auditing for free instead of each editing 30 handlers.
   Tracks add at most a one-line "summary/snapshot" hook for their new actions.
2. **Deploy-trigger + deployment-history schema decisions** (from `01`/`02`). Lock the schema
   shape (trigger rules; `rolled_back_from`) before Track A starts, so the migrations don't
   churn mid-flight.

After Stage 0, the tracks below run concurrently.

---

## Track A — Deploy Core *(critical path, sequential)* — 🟡 A1+A2 done, A3 deferred

**Owns:** `api/internal/deploy`, `caddy`, `proxy`, deployments store, the Deploys tab UI.
**Why sequential:** all three rewrite the same pipeline; parallelizing them is a merge tar pit.

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| A1 | `02` Rollback | S–M | ✅ done | start here — cheapest, highest trust, unblocks safe iteration |
| A2 | `01` Push-to-deploy | L | ✅ done | the flagship; build on A1 so auto-deploys have an undo |
| A3 | `05` Zero-downtime | L | ⏸ deferred | hardest; only after A1+A2 are solid — design in [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md) |

This is the **needle-mover track** — staff it with your strongest deploy-pipeline person.

## Track B — Observability & Limits *(parallel)* — ✅ done

**Owns:** `stats`, `reqmetrics`, host stats, `config`/build (`GOMEMLIMIT`, Makefile, CI),
dashboard meters UI. **Near-zero overlap with Track A.**

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| B1 | `07` RAM benchmark harness | S–M | ✅ done | do first — guards the headline claim before weight piles on |
| B2 | `13` Prometheus exposition | S–M | ✅ done | reuses stats/reqmetrics; standalone-useful; unblocks 12 |
| B3 | `06` Resource guardrails | M | ✅ done | per-app limits + box budget + OOM detection |

## Track C — Trust & Lifecycle *(parallel)* — ✅ done

**Owns:** auth/middleware (after Stage 0), `notify`, `caddy` PKI read, `routes/setup` UI,
activity-feed UI. **Mostly additive; low collision.**

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| C1 | `11` Revert (Part 2) | M | ✅ done | layers onto the Stage-0 audit log; snapshot-based undo for config |
| C2 | `03` Cert-expiry notification | S | ✅ done | needs Caddy per-host `not_after`; wire into existing dispatcher |
| C3 | `04` Onboarding wizard | M | ✅ done | UI-heavy; do once Track A's core loop is demoable |

> Note: C1's revert of *deploys* leans on A1 (rollback). If Track A hasn't shipped A1 yet,
> C1 can still do config/env/domain revert and defer deploy-revert to "see A1."

## Track D — Managed Services *(parallel, greenfield)*

**Owns:** new packages (backup scheduler, DB provisioning, add-on template registry) + their
UI. **Greenfield → lowest collision with existing code.** Internally sequenced by dependency.

| Order | Item | Effort | Note |
|---|---|---|---|
| D1 | `08` Managed backups | M | engine-agnostic dump primitive; foundation for D2/D3 |
| D2 | `09` Managed DBs | L | one shared instance per engine, lazy start (see stub) |
| D3 | `12` Add-on catalog (Grafana) | M | depends on D2 (DB dashboards) + benefits from B2 (13) |

> Strategy gate: D is the monetization arc — don't *ship* it until Tracks A/B are trustworthy
> (you're selling reliability). But it can be *built* in parallel since it barely touches the
> core.

## Track E — Managed VAC *(future, separate repo)*

`10` Managed VAC provisioning is a **separate hosted orchestrator**, not part of `vac-api`.
Genuinely parallel because it's a different codebase — but XL, depends on everything else
being solid, and needs a business (billing/support) behind it. Not now; tracked for completeness.

---

## Cross-track sync points

- **Schema migrations** are the main collision risk — they share one numbered sequence
  (`api/internal/db/migrations/`). Coordinate migration numbers across tracks (assign ranges
  per track, or rebase migration numbers at merge). This is the one place parallel work bites.
- **`13` (Track B) ↔ `12` (Track D):** 12's "charts about VAC" uses 13's `/metrics`. If 13
  lags, 12 uses its lightweight path (Grafana → Postgres) and adopts Prometheus later.
- **`02` (Track A) ↔ `11`-revert (Track C):** deploy-revert is rollback; config-revert is
  independent. Keep them decoupled so neither blocks the other.

## Staffing guide

- **1 person:** just follow Track A in order (`02 → 01`), then cherry-pick `07` and `11`-Part-1
  between. Ignore the track structure — it's for parallelism you don't have yet.
- **2 people:** Track A (strongest dev) ‖ Track B. Pull C/D items in as A/B free up.
- **3+ people / parallel agents:** A ‖ B ‖ (C+D share a person, or split). Stage 0 first,
  watch migration numbering.

## The critical path

**Track A is the spine.** B, C, D make the product more trustworthy, observable, and
monetizable — but A (`02 → 01`) is what makes it *feel like a platform*. If a track must slip,
slip D before C before B before A.
