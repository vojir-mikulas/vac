# 02 — Rollback

**Tier:** Close the loop · **Effort:** S–M · **Status:** stub

## Goal

One-click "redeploy this previous deployment." Undo for deploys.

## Why it matters (strategy)

Reliability-as-UX, dead center of the moat. People deploy braver when undo is one click;
it's the safety net that makes aggressive deploys (incl. zero-downtime, plan 05)
emotionally safe.

## Rough shape

- The data is already there: deployment history + image retention (last 3 per service).
- "Redeploy" = re-run the deploy pipeline pinned to a previous deployment's commit
  SHA / compose hash, reusing the retained image rather than rebuilding where possible.
- A rollback should itself be recorded as a new deployment (audit trail), not a mutation
  of history — e.g. `triggered_by: rollback, rolled_back_from: {deployment_id}`.

## Open questions

- Rebuild from SHA vs. reuse the existing image tag — image reuse is faster and truer to
  "rollback," but interacts with the image-retention pruner (don't prune an image a user
  might roll back to — consider pinning the active + last-good).
- Env-var drift: do we roll back env vars too, or only code? (Probably code only, with a
  warning if env changed since.)
- Interaction with rollback when the previous image was already pruned.

## Acceptance (sketch)

- From the Deploys tab, selecting a prior successful deployment and clicking "Roll back"
  brings that version live and records a new deployment row referencing the source.
