# Done — shipped plans

Plans whose work has landed. Moved here from [`../upcoming/`](../upcoming/) once implemented so
that folder only shows not-yet-built work. The stub files are kept verbatim as the record of
original intent; the as-built reconciliation lives in the Track-A execution doc and
`docs/deviations.md`.

| # | File | Track | What shipped |
|---|------|-------|--------------|
| 02 | [02-rollback.md](02-rollback.md) | A1 | One-click redeploy pinned to a prior deployment's commit SHA (rollback). |
| 01 | [01-push-to-deploy.md](01-push-to-deploy.md) | A2 | Git-webhook auto-deploy: per-app trigger rules + payload-authenticated inbound webhook. |

Execution detail (A1 + A2, plus the still-deferred A3 design):
[`../upcoming/A-deploy-core-execution.md`](../upcoming/A-deploy-core-execution.md).

> A3 (zero-downtime / rolling deploys) is **not** here — it's deferred. Its detailed,
> spike-gated design is in [`../upcoming/A3-zero-downtime-detail.md`](../upcoming/A3-zero-downtime-detail.md).
