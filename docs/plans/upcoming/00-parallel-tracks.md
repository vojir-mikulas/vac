# 00 — Parallel execution tracks

How to run the upcoming stubs as **concurrent tracks**. The grouping rule is *subsystem
ownership*: each track owns a distinct slice of the codebase so two tracks rarely touch the
same files. Items **within** a track are sequenced (they share files or build on each other);
items **across** tracks run in parallel.

The one hard constraint that drives everything: **the deploy pipeline (`api/internal/deploy`,
`caddy`, `proxy`) is a single hot path.** Push-to-deploy, rollback, and zero-downtime all
rewrite it — so they must be *one* track done in order, not parallel work, or you get merge
hell. Everything else is arranged to stay out of that path.

```
          ┌─ Track A: DEPLOY CORE ──────────── ✅02 → ✅01 → ⏸05     (critical path; 05 deferred)
          │
  done    ├─ Track B: OBSERVABILITY & LIMITS ── ✅07 → ✅13 → ✅06    (complete)
  (S0) ───┤
          ├─ Track C: TRUST & LIFECYCLE ─────── ✅11 → ✅03 → ✅04    (complete)
          │
  done    ├─ Track D: MANAGED SERVICES ──────── ✅08 → ✅09 → ✅12     (shipped; Mongo/Redis to add)
          │
 active   ├─ Track E: TRUST & SAFETY ────────── 16 ‖ 15              (active)
          │
   done   └─ Track F: DEV-EXPERIENCE ────────── ✅14                  (shipped)

 dropped    Track ⨯: MANAGED VAC (separate repo) ─ 10                (dropped for now)
```

> **Status (2026-06-02):** Stage 0 + **Tracks B, C, D, and F complete** (all moved to
> [`../done/`](../done/)). **Track A** shipped A1+A2 (rollback + push-to-deploy); **A3
> zero-downtime is deferred/paused** (design in [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md)).
> **Track D** (managed services) shipped `08`/`09`/`12` behind `VAC_MANAGED_SERVICES` — managed DBs
> currently cover **Postgres + MariaDB** (Mongo/Redis still to add). The remaining active track is
> **Track E — Trust & Safety** (`16` compose-preflight, `15` security dashboard), plus standalone
> `18` (portability). **The Managed-VAC track (`10`) is dropped for now** — see the tombstone below.

---

## Stage 0 — landed (shared seams) ✅ *done*

Two foundations built up front so the tracks proceed without colliding:

1. **Audit middleware seam** (from `11`, Part 1). The `audit_log` table + a *central middleware*
   that records actor/route/outcome for every mutating request — tracks inherit auditing for
   free instead of each editing 30 handlers.
2. **Deploy-trigger + deployment-history schema** (from `01`/`02`). Schema shape (trigger rules;
   `rolled_back_from`) locked before Track A started.

---

## Track A — Deploy Core *(critical path, sequential)* — 🟡 A1+A2 done, A3 deferred

**Owns:** `api/internal/deploy`, `caddy`, `proxy`, deployments store, the Deploys tab UI.
**Why sequential:** all three rewrite the same pipeline; parallelizing them is a merge tar pit.

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| A1 | `02` Rollback | S–M | ✅ done | cheapest, highest trust; unblocked safe iteration |
| A2 | `01` Push-to-deploy | L | ✅ done | the flagship; built on A1 so auto-deploys have an undo |
| A3 | `05` Zero-downtime | L | ⏸ deferred | hardest; only after A1+A2 prove solid — design in [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md) |

This was the **needle-mover track**. Only A3 remains, and it's parked. Note: Track E's `16`
inserts one *additive* gate into `deploy/pipeline.go` — see cross-track sync for how that stays
clear of A3.

## Track B — Observability & Limits *(parallel)* — ✅ done

**Owns:** `stats`, `reqmetrics`, host stats, `config`/build (`GOMEMLIMIT`, Makefile, CI),
dashboard meters UI.

| Order | Item | Effort | Status |
|---|---|---|---|
| B1 | `07` RAM benchmark harness | S–M | ✅ done |
| B2 | `13` Prometheus exposition | S–M | ✅ done |
| B3 | `06` Resource guardrails | M | ✅ done |

## Track C — Trust & Lifecycle *(parallel)* — ✅ done

**Owns:** auth/middleware, `notify`, `caddy` PKI read, `routes/setup` UI, activity-feed UI.

| Order | Item | Effort | Status |
|---|---|---|---|
| C1 | `11` Revert (Part 2) | M | ✅ done |
| C2 | `03` Cert-expiry notification | S | ✅ done |
| C3 | `04` Onboarding wizard | M | ✅ done |

## Track D — Managed Services *(parallel, greenfield)* — ✅ shipped (behind `VAC_MANAGED_SERVICES`)

**Owns:** new packages (backup scheduler, DB provisioning, add-on template registry) + their
UI. **Greenfield → lowest collision with existing code.** Internally sequenced by dependency.
**Detailed execution plan:** [`../done/D-managed-services-execution.md`](../done/D-managed-services-execution.md)
(migration range `00040`–`00049`, scheduler/destination/engine decisions locked).

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| D1 | `08` Managed backups | M | ✅ done | `internal/backup`, migration `00040` |
| D2 | `09` Managed DBs | L | ✅ done | `internal/dbprovision`, migration `00041` — **Postgres + MariaDB live; Mongo/Redis to add** |
| D3 | `12` Add-on catalog (Grafana) | M | ✅ done | `internal/addon`, migration `00042`, Grafana template |

> Strategy gate (as built): D shipped **gated off by default** (`VAC_MANAGED_SERVICES`), keeping
> the `<200 MB` control-plane claim honest — none of D's background goroutines start when it's off.

## Track E — Trust & Safety *(new, parallel)*

**Owns:** `api/internal/compose` (new `preflight.go`), one insertion point in
`deploy/pipeline.go`, a new security package + Caddy access-log config, and a new Security UI
tab. The two items are file-disjoint (compose/pipeline vs. security-pkg/UI), so **unlike a
normal track they can be split across two agents** — they're listed in priority order, not
because they share files.

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| E1 | `16` Compose preflight validation | M | stub | do first — deploy-path safety; blocks/​warns on edge-port, bundled-proxy, docker.sock, host-ports before Build |
| E2 | `15` Security dashboard | M | stub | read-only posture + traffic-anomaly panels; reuses Track B's `stats`/`reqmetrics`/`metrics`/`notify` |

> Both are **trust-moat** items (the moat is simplicity + UX + reliability + trust, not feature
> count). E1 hardens the deploy loop against foot-guns; E2 turns Caddy + the build pipeline —
> VAC's two best vantage points — into a "your box at a glance" security view. Neither mutates
> host state (E2 is read-only by design), keeping the control plane sandboxed.

## Track F — Dev-Experience *(parallel, isolated)* — ✅ shipped

**Owns:** `.github/` only (`workflows/`, a new composite action). **Zero overlap with any other
track's source.** Safe to land any time.

**Detailed execution plan:** [`../done/F-dev-experience-execution.md`](../done/F-dev-experience-execution.md).
The four cleanups landed in `8e41215`, were disabled in `4893ec5`, and are **re-enabled** in
this track after a local green pass — see the execution plan for the activation gate (the
`schedule`/`push:main` triggers only fire from the file once it's on `main`).

| Order | Item | Effort | Status | Note |
|---|---|---|---|---|
| F1 | `14` CI / Actions cleanup | S | ✅ done | cleanup shipped in `8e41215`; triggers re-enabled (push/PR + bench-ram main/nightly) after local green pass |

## Track ⨯ — Managed VAC *(dropped for now)*

`10` Managed VAC provisioning (a **separate hosted orchestrator**, not part of `vac-api`) is
**dropped from the active plan.** It's XL, lives in a different codebase, depends on everything
else being solid, and needs a business (billing/support) behind it. The stub
([`10-managed-vac-provisioning.md`](10-managed-vac-provisioning.md)) is retained for
completeness; revisit only once the product moat (A/B/C/E) is proven.

---

## Cross-track sync points

- **Schema migrations** are the main collision risk — they share one numbered sequence.
  **Track D landed `00040`–`00042`**, so any migration added by Track E must start at `00043`+
  to avoid clashing. This is the one place parallel work bites.
- **`16` (Track E) ↔ `05`/A3 (Track A):** `16` inserts a single *additive* gate into
  `deploy/pipeline.go` (right after `Prepare` resolves the compose, before Build) — it does
  **not** rewrite the up→health→swap path that A3 changes, so they don't conflict today. A3 is
  deferred anyway; if it resumes, land `16` first (or rebase its one insertion). This is the
  only point where Track E touches Track A's hot path.
- **`15` (Track E) ↔ Track B (done):** the security dashboard's traffic panel reuses the
  shipped `stats`/`reqmetrics`/`metrics`/`notify` plumbing — additive consumer, no rewrite.
- **`14` (Track F)** touches only `.github/` — no source-code sync needed; land independently.

## Staffing guide

With Tracks B/C/D/F shipped and A3 parked, the remaining work is **Track E** (`16` → `15`) and
standalone `18` (portability).

- **1 person:** pick the highest-leverage item: `16` (deploy-path safety) first, then `15`, then
  `18`. Ignore the track structure — it's for parallelism you don't have yet.
- **2+ people / parallel agents:** E1 `16` ‖ E2 `15` ‖ `18` run concurrently (file-disjoint).
  Start any new migration at `00043`+.

## The critical path

With A1+A2 shipped, A3 parked, and Tracks B/C/D/F landed, the spine is built. The live priority
is **Track E** (`16` then `15`) for the trust moat, with `18` (portability) as the other trust
play. If work must slip, protect the trust-moat items.
