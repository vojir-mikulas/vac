# Track A — Deploy Core · Execution Plan

**Owns:** `api/internal/deploy`, `caddy`, `proxy`, deployments store + handler, the
Deploys tab UI. **Sequential** — A1 → A2 → A3 all rewrite the one deploy pipeline; doing
them in order avoids a merge tar pit.

Branch: built in the current worktree (`proof-sage/vac`, detached from `main`), merged to
`main` later. Stage 0 (audit seam + deploy schema lock, commit `3fe8a37`) is already landed
and is the foundation this builds on.

> Status legend: ⬜ not started · 🟡 in progress · ✅ done

| Item | Effort | Status |
|---|---|---|
| **A1** `02` Rollback | S–M | ✅ done — shipped stub in [`../done/02-rollback.md`](../done/02-rollback.md) |
| **A2** `01` Push-to-deploy | L | ✅ done — shipped stub in [`../done/01-push-to-deploy.md`](../done/01-push-to-deploy.md) |
| **A3** `05` Zero-downtime | L | ⬜ deferred — design in [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md), evaluate later |

> **A1 + A2 are implemented** (working tree, pending commit). A3 is the remaining item and is
> intentionally **deferred** — it's spike-gated and large. This doc stays here while A3 is open;
> the A1/A2 sections below are kept as the execution record.

---

## What Stage 0 already gives us (don't rebuild)

- `deployments.triggered_by` (`manual|push|tag|rollback|system`) + `rolled_back_from UUID`
  FK (`ON DELETE SET NULL`). Store constants in `store/deployments.go`.
- `deploy_triggers` table + `store.{List,Create,Delete}DeployTrigger` + event constants
  (`push|tag|manual`). Schema seam for A2; matching engine not yet built.
- `CreateDeployment(ctx, appID, triggeredBy, rolledBackFrom)` already takes both fields.
- Central audit middleware: handlers enrich with `audit.SetTarget` / `audit.Describe` /
  `audit.SetMetadata`. Every mutating route is audited for free.
- Append-only deployment history (never pruned), `commit_sha` + `compose_hash` per row.

## Pipeline facts that constrain the design

- `Pipeline.Run(ctx, deploymentID)` pulls **HEAD** of `app.GitBranch` every time
  (`cloneOrPull`). There is no "deploy this exact commit" path today.
- `docker compose build` tags images `<project>-<service>:latest` and **overwrites them
  each build**. Retained per-deployment images do not exist yet → image-reuse rollback is
  not free.
- `vac-api` is off `vac-edge`; health is gated through Caddy's `/reverse_proxy/upstreams`.
  Routing is by DNS alias `{slug}--{service}` attached **after** `compose up`, in
  `proxy.Manager.Sync`. Deploy failure never tears down the prior stack.
- One worker, one in-flight deploy (`deploy.Worker`, queue cap 32).

---

## A1 — Rollback  *(start here)*

**Goal:** From the Deploys tab, pick a prior successful deployment → "Roll back" → that
version comes live, recorded as a **new** deployment with `triggered_by=rollback` and
`rolled_back_from=<source id>`. History is append-only.

**Design decision — rebuild from pinned SHA (not image reuse).**
Image reuse needs per-deployment image tagging + retention-pinning we don't have, and
interacts badly with the pruner. Re-running the pipeline pinned to the source deployment's
`commit_sha` is simpler, truer to "redeploy that version", and reuses the entire existing
health-gated pipeline. Image-tag reuse is a later optimization (note it in deviations).
**Env vars are NOT rolled back** (code only) — warn in the UI if env changed since.

### Steps

1. **gitcli: pin to a commit.** Add `Checkout(ctx, dir, sha)` (`git checkout --detach <sha>`)
   to `gitcli` and to the `deploy.GitClient` interface + `realGit`. Fakes in tests get a
   no-op/record.
2. **Pipeline accepts a target commit.** Thread the deployment's `commit_sha` into `Run`:
   after `cloneOrPull`, if the deployment row has a pinned SHA *and* it was a rollback,
   `git fetch` + `Checkout` to it before reading HEAD. Cleanest seam: read
   `d.TriggeredBy == rollback && d.CommitSHA != nil` inside `Run` (the row already carries
   the SHA we copy in at enqueue time). No new pipeline parameter needed.
3. **Store: copy source SHA at enqueue.** New `store.CreateRollbackDeployment(ctx, appID,
   sourceID)` that loads the source deployment, validates it belongs to the app and was
   `running`, and inserts a row with `triggered_by=rollback`, `rolled_back_from=sourceID`,
   and `commit_sha`/`commit_message` pre-seeded from the source. (Pre-seeding the SHA is
   what lets `Run` pin without an extra column.)
4. **Handler + route.** `POST /api/apps/{id}/deployments/{did}/rollback` → validates source,
   `CreateRollbackDeployment`, `Enqueue`, audit `Describe("rolled back {slug} to {sha}")` +
   `SetTarget("app", id)`. 202 with the new deployment DTO. Guard rails: refuse if source
   isn't a successful deploy of this app; refuse self-rollback of an in-flight deploy.
5. **UI.** "Roll back" button on each successful `DeployRow` (not the live/current one).
   Confirm dialog noting "redeploys this commit; env vars are not changed." New
   `rollbackDeployment(appId, did)` in `ui/src/lib/api/deployments.ts`; on success it
   refetches the list (the new deployment appears at top and streams logs like any deploy).
   Show a small "⤺ rolled back from <sha>" badge on rollback rows (`rolled_back_from`).
6. **Pruner guard (small).** Don't let the deployment/image pruner remove the image/row a
   user could roll back to. Today deployments aren't pruned (safe). Add a code comment +
   keep "last good + active" pinned when image tagging lands (A3). For A1, rebuild-from-SHA
   means even a pruned image is fine — it rebuilds. **No pruner change needed for A1.**

### Tests
- `gitcli` checkout (integration, real repo).
- Store: `CreateRollbackDeployment` copies SHA, sets `rolled_back_from`, rejects
  cross-app / non-running source.
- Pipeline unit: a rollback deployment with a pinned SHA calls `Git.Checkout` with that SHA.
- Handler: 202 happy path, 404 unknown source, 409/422 invalid source.

### Acceptance
Selecting a prior successful deployment and clicking "Roll back" brings that commit live and
records a new deployment row referencing the source. ✅ when green + `make test` passes.

---

## A2 — Push-to-deploy  *(build on A1)*

**Goal:** `git push` → VAC deploys, no button. Per-app trigger rules; inbound webhook
endpoint with signature verification; ignored pushes logged.

### Steps

1. **Trigger matching engine** (`deploy` or new `trigger` pkg, pure + unit-tested):
   `Match(rules []store.DeployTrigger, event, ref) bool`. `event∈push|tag`; `ref` is the
   branch/tag short name. `filter` is a glob (`main`, `release/*`, `v*`); `''` matches any
   ref of that type. Tag pushes only fire `tag` rules, branch pushes only `push` rules.
2. **Webhook secret storage.** Per-app secret, encrypted with `crypto.Box` like other
   secrets. New column or reuse a secrets table — **decision:** add `apps.webhook_secret`
   (encrypted bytes) via a new migration `00021`, generated lazily on first trigger-rule
   creation. Surface only the masked secret + a "regenerate" action.
3. **Inbound endpoint** `POST /api/webhooks/{appID}` — **unauthenticated** (outside the
   session/CSRF group; its own router mount), rate-limited, body-size-capped:
   - **Generic first:** JSON `{ ref, ref_type }` or query params; verified by a shared
     secret (constant-time compare of `X-VAC-Token` or `?token=`).
   - **GitHub/GitLab:** `X-Hub-Signature-256` (HMAC-SHA256 of body) / `X-Gitlab-Token`.
     Parse `ref` (`refs/heads/x` → push:x, `refs/tags/v1` → tag:v1).
   - On match → `CreateDeployment(triggered_by=push|tag)` + `Enqueue`. On no-match → 200 +
     audit/activity line "ignored push to <ref> (no matching rule)". Debounce/coalesce
     rapid pushes (skip if an identical-ref deploy is already queued/running).
4. **Trigger-rule CRUD** handlers + routes under the app: list/create/delete
   `deploy_triggers`. `apps.git_branch` becomes "default branch for manual deploys" (no
   schema change — just framing in UI copy).
5. **UI — Settings → Source.** Show inbound webhook URL + masked secret (copy/regenerate),
   and a small editor for trigger rules (event + filter). Activity feed surfaces ignored
   pushes.
6. **Audit/activity.** Webhook-triggered deploys are `actor_type=system` (or a dedicated
   `webhook` actor) in the audit log; ignored pushes recorded with a summary.

### Decisions (confirmed)
- Webhook URL shape: **`/webhooks/{appID}` — appID in path** (top-level, *not* under
  `/api`, so it sits outside the session Auth+CSRF group and avoids a chi mount conflict).
  Revocable via regenerate. Confirmed 2026-06-01.
- Credentials are accepted from **headers only** (GitHub `X-Hub-Signature-256` HMAC, GitLab
  `X-Gitlab-Token`, generic `X-VAC-Token`) — never `?token=`, so the secret can't leak into
  proxy/access logs (security-review finding).
- One secret per app (not per provider). Trigger deletes are app-scoped in the store so one
  app can't remove another's rule.

### Tests
- Matching engine table tests (globs, event/ref-type pairing, empty filter).
- Signature verification (GitHub HMAC fixture, GitLab token, generic).
- Endpoint: matching ref deploys; non-matching ignored+logged; bad signature → 401;
  tag-only app deploys on `v1.2.3` not on branch push; debounce dedupes.

### Acceptance
A push to a matching branch auto-creates a deployment; a non-matching ref is ignored and
logged. Tag-only apps deploy on `v1.2.3` but not on a branch push.

---

## A3 — Zero-downtime / rolling deploys  *(hardest; only after A1+A2 solid)*

**Goal:** No 502s through a successful redeploy of a **stateless HTTP service**. Bring up
new alongside old → Caddy sees new healthy → swap upstream → drain → remove old.

**Full design + sequenced steps:** [`A3-zero-downtime-detail.md`](A3-zero-downtime-detail.md).
Summary:

- **Core tension:** `compose up -d` recreates a service in place (stops old → starts new under
  the same vac-edge alias), so the alias points at nothing during the gap → 502. A3 removes
  that gap. Routing-by-alias + Caddy-owned health already make the cutover a Caddy admin-API
  op; the only missing piece is running two generations of one service at once.
- **Invariant:** only **stateless HTTP services** are rollable (`internal_port != nil`, no
  `volumes:`, single replica). Stateful services (single-writer DBs) can't be rolled and are
  recreated in place as today.
- **Spike-gated:** the mechanism for "new + old of one stateless service simultaneously" is the
  one real unknown. **Spike first** (M1 compose `--scale`+image-ID side-by-side vs. M2
  VAC-managed `docker run` generation containers), success = **0 non-200s across cutover** +
  clean renormalization. The detail doc has the exact spike definition and the
  mechanism-independent pieces (generation-alias routing + atomic upstream swap, drain window,
  pipeline branch, never-502 failure handling that composes with A1, schema `00022`, config,
  UI, tests).

### Acceptance
A redeploy of a stateless HTTP service serves continuously (no 502s) through the cutover.

---

## Cross-cutting

- **Migrations:** Track A owns `00021+`. A2 added `00021` (`apps.webhook_secret_enc`); A3
  reserves `00022` (`services.route_alias`). Coordinate numbers if other tracks merge first
  (rebase numbers at merge).
- **KB:** A2/A3 change the deploy pipeline → regenerate `docs/kb/deployment-flow.md`
  (run `/refresh-kb`) and log image-tag-reuse deferral in `docs/deviations.md`.
- **Each item:** `make test` + `make lint` + `make typecheck` green; run `/code-review` and
  `/simplify` before marking done; propose a Conventional Commit at each item boundary.
- **Checkpoints:** review at the end of A1 before starting A2; spike A3 before committing to
  its step list.
