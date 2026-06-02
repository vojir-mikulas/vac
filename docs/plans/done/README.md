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
| 08 | [08-managed-backups.md](08-managed-backups.md) | D1 | Scheduled per-service backups via `docker exec` dump → S3/B2/local (`internal/backup`, gated by `VAC_MANAGED_SERVICES`). |
| 09 | [09-managed-databases.md](09-managed-databases.md) | D2 | One-click managed DBs with connection-string injection (`internal/dbprovision`) — **Postgres + MariaDB live; Mongo/Redis still to add**. |
| 12 | [12-addon-templates-catalog.md](12-addon-templates-catalog.md) | D3 | One-click add-on template catalog deployed through the normal pipeline (`internal/addon`); Grafana flagship. |
| 14 | [14-ci-workflow-cleanup.md](14-ci-workflow-cleanup.md) | F1 | bench-ram off PRs, DRY composite setup action, consolidated `release.yml`. |
| 17 | [17-installer-overhaul.md](17-installer-overhaul.md) | — | Guided first-run installer wizard + readable `main()` + `vac managed-services on\|off` toggle. |

Execution detail: Track A (A1 + A2, plus the still-deferred A3 design) in
[`../upcoming/A-deploy-core-execution.md`](../upcoming/A-deploy-core-execution.md); Track B in
[`track-b-execution.md`](track-b-execution.md); Track D in
[`D-managed-services-execution.md`](D-managed-services-execution.md); Track F in
[`F-dev-experience-execution.md`](F-dev-experience-execution.md).

> A3 (zero-downtime / rolling deploys) is **not** here — it's deferred. Its detailed,
> spike-gated design is in [`../upcoming/A3-zero-downtime-detail.md`](../upcoming/A3-zero-downtime-detail.md).
