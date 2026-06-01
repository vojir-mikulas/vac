# Done — shipped plans

Plans whose work has landed. Moved here from [`../upcoming/`](../upcoming/) once implemented so
that folder only shows not-yet-built work. The stub files are kept verbatim as the record of
original intent; the as-built reconciliation lives in the Track-A execution doc and
`docs/deviations.md`.

| # | File | Track | What shipped |
|---|------|-------|--------------|
| 02 | [02-rollback.md](02-rollback.md) | A1 | One-click redeploy pinned to a prior deployment's commit SHA (rollback). |
| 01 | [01-push-to-deploy.md](01-push-to-deploy.md) | A2 | Git-webhook auto-deploy: per-app trigger rules + payload-authenticated inbound webhook. |
| 07 | [07-ram-benchmark-harness.md](07-ram-benchmark-harness.md) | B1 | Repeatable, CI-enforced idle-RAM measurement guarding the <200 MB claim. |
| 13 | [13-prometheus-metrics-exposition.md](13-prometheus-metrics-exposition.md) | B2 | VAC metrics exposed on a Prometheus `/metrics` endpoint. |
| 06 | [06-resource-guardrails.md](06-resource-guardrails.md) | B3 | Per-app RAM limits + box-level budget UI + OOM detection. |
| 11 | [11-audit-log-and-revert.md](11-audit-log-and-revert.md) | C1 | Audit log (Activity feed) + curated revert of safely-invertible actions. |
| 03 | [03-cert-expiry-notification.md](03-cert-expiry-notification.md) | C2 | Cert-expiry checks (`internal/certcheck`) wired into the notification dispatcher (resolves D7). |
| 04 | [04-onboarding-wizard.md](04-onboarding-wizard.md) | C3 | First-run onboarding checklist guiding connect-repo → first-deploy. |

Execution detail: Track A (A1 + A2, plus the still-deferred A3 design) in
[`../upcoming/A-deploy-core-execution.md`](../upcoming/A-deploy-core-execution.md); Track B in
[`../upcoming/track-b-execution.md`](../upcoming/track-b-execution.md).

> A3 (zero-downtime / rolling deploys) is **not** here — it's deferred. Its detailed,
> spike-gated design is in [`../upcoming/A3-zero-downtime-detail.md`](../upcoming/A3-zero-downtime-detail.md).
