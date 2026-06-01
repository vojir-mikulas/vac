# 11 — Audit log + curated revert

**Tier:** Close the loop / Reliability moat · **Effort:** audit S–M, revert M · **Status:** stub

## Goal

Two related things:
1. **Audit log** — a durable record of every major action on the box: *who* did *what*,
   *when*, from *where*. Designed to scale to multiple users later.
2. **Curated revert** — one-click undo for the *config/data* actions where an inverse can
   safely exist. Explicitly **not** universal undo.

## Why it matters (strategy)

Trust. Operators (and later, teams) need to see and reverse changes confidently. Reverting
is reliability-as-UX. Both are "remove operational pain / developer trust."

## Part 1 — Audit log (easy; build first)

Attribution is essentially free: every mutating request already resolves an actor in auth
middleware (session → `user_id`, or API token → `user_id`).

- New table `audit_log`: `id, actor_user_id, actor_type (user|api_token|system), action,
  target_type, target_id, summary, metadata (JSONB), ip, user_agent, created_at`.
- Emit one event per mutating handler (or a thin middleware keyed on route + actor). ~30
  handlers, mostly one line each.
- **Supersedes the current activity feed** (today derived from deployments/events, no real
  table). Reuse `activity_retention_days` for pruning.
- Multi-user-ready by construction: `actor_user_id` just starts varying.

## Part 2 — Curated revert (medium; per-action)

Revertability is a spectrum, not a switch. Tier the actions:

| Action | Revertable? | Mechanism |
|---|---|---|
| Deploy | ✅ | redeploy prior version → this is plan **02 (rollback)** |
| Env var / domain / settings / app-config change | ✅ | store a **before-snapshot** in the audit row; reapply |
| App delete (`compose down -v`) | ⚠️ only if **soft** | opt-in soft-delete + retain volumes for a grace period, then revertable |
| Instance reset | ❌ | destructive by design; never faked as revertable |

- Model each mutation as a **command that stores its inverse** (a "before" snapshot or a
  typed compensating action). Revert = apply the inverse.
- Where no inverse can exist (data destroyed), flag the audit row `revertable: false`; the
  UI greys out undo.
- **Anti-goal:** full event-sourcing / "everything undoable." That's the complexity
  explosion the strategy warns against. Curate the high-value, safely-invertible set.

## Open questions

- Snapshot storage: inline JSONB in the audit row vs. a side table for large before/after.
- Soft-delete grace period length + disk cost of retained volumes.
- Whether revert is itself an audited action (it should be).

## Acceptance (sketch)

- Every mutating action appears in the audit log attributed to an actor.
- An env-var change can be one-click reverted; a destructive op is clearly marked
  non-revertable (or revertable only within its soft-delete grace window).
