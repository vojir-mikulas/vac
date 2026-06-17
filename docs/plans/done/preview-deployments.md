# Preview Deployments — design sketch

Vercel-style per-branch ephemeral environments on a single box. A push to a non-default branch
(or an opened PR) spins up a throwaway stack on a derived preview URL; it's torn down when the
branch/PR closes. The honest tension: **a preview is a full second compose stack on the same VPS** —
same RAM, same disk, same CPU — so the design is as much about *guardrails* as it is about
plumbing.

Status: **planned** (not started).

The cheapest correct shape: **a preview is just an app** — `is_preview=true`,
`parent_app_id` set, its own derived slug `{parent-slug}-{branch}`. It reuses the entire existing
pipeline (clone → build → up → attach vac-edge → health → route → running) untouched, gets its
own compose project `vac-{preview-slug}`, its own vac-edge aliases, and its own rowless auto-host.
No second deploy engine, no second router, no second teardown path.

## Why

VAC already has all three legs of this feature standing separately and never wired together:
push-to-deploy webhooks (`deploy_triggers`), a slug-keyed compose stack per app, and rowless
derived auto-hosts. What's missing is the lifecycle that connects "a branch appeared" to "a stack
exists at a URL" to "the branch is gone, reclaim it." Preview environments are the single most-asked
PaaS feature after custom domains, and on a one-operator box they're genuinely useful: review a
feature branch at a real HTTPS URL before merging, without touching production.

The architecturally honest part — and why this is the hardest plan in the set — is that **the
single box forbids the thing that makes Vercel previews cheap** (infinite ephemeral lambdas). Every
preview here is a real long-lived stack competing for the `<200 MB idle` budget. So the plan leads
with guardrails, not features.

## Key technical realities (read before building)

- **A preview is a separate compose stack, full stop.** Project name is
  `composeProject(slug)` → `"vac-" + slug` (`deploy/pipeline.go:638`). Give a preview a distinct
  slug and it gets a distinct project, distinct containers, distinct named volumes — completely
  isolated from the parent at the Docker level for free. This is why "preview = app with a derived
  slug" is the right model: zero new isolation code.
- **Branch already flows through the pipeline.** `apps.git_branch` (`store/apps.go:29`) is passed
  to `Git.Clone`/`Git.Pull` (`deploy/pipeline.go:502,507`); `gitcli.Clone` takes a `branch`
  parameter and shallow-clones `--single-branch` (`gitcli/gitcli.go:74`). A preview app is the
  *same parent repo URL* with a *different `git_branch`* — nothing in the pipeline needs to change.
- **The auto-host scheme is dots, the network alias is dashes — don't conflate them.**
  `AutoSubdomain` derives `{slug}.{base}` (single HTTP service) or `{service}.{slug}.{base}`
  (multi-service) (`proxy/hostname.go:22-30`). The `--` form (`{slug}--{service}`) is the *vac-edge
  DNS alias* (`proxy/network.go:8-14`), an internal name Caddy dials, never a hostname. A preview's
  URL falls out of `AutoSubdomain` automatically the moment its slug differs — **no AutoHosts change
  needed** if the preview slug is `{parent}-{branch}`. (See scope decision #2 for the slug→host
  shape and the one subtlety.)
- **Auto-hosts are rowless and self-pruning.** They're a pure function of (slug, HTTP services, base
  domain) computed at reconcile (`deviations.md` F1; `proxy/manager.go` `AutoHosts`), routed under
  `@id` `vac-auto-{appID}-{service}`, and `pruneOrphans` deletes any `vac-auto-*`/`vac-route-*` route
  not backed by current state (`proxy/network.go:24-39`, `manager.go:418-448`). A preview slots into
  this verbatim: it gets `vac-auto-{previewAppID}-{service}` routes for free, and **teardown of the
  preview app makes those routes structurally orphaned** — the next reconcile prunes them. No
  bespoke route cleanup.
- **CaddyAsk already authorizes derived auto-hosts** via `IsAutoHost` (`handler/caddy_ask.go`,
  `manager.go` `IsAutoHost`). A preview's `{parent}-{branch}.{base}` host is a derived auto-host, so
  on-demand TLS issuance is authorized with no new ask path — *provided* the operator's wildcard
  cert / on-demand setup covers the deeper label (decision #2 caveat).
- **Webhooks today only see push and tag — no PR, no branch-delete.** `ParseRef` maps `refs/heads/*`
  → push and `refs/tags/*` → tag (`webhook/webhook.go:41-50`); `MatchTriggers` does glob matching on
  the short name (`webhook.go:55-65`). There is **zero** handling of `pull_request` payloads, branch
  *deletion* (a push with a zero after-SHA), or PR-close (grep: no `pull_request`/`deleted`/`closed`).
  The teardown trigger is genuinely new parsing work, not a tweak.
- **Teardown already exists — for apps.** App delete runs `ctrl.Down("vac-"+app.Slug, true)` then
  `DeleteApp` which cascades services/deployments/env/domains/managed_dbs
  (`handler/apps.go:435`; FK `ON DELETE CASCADE`). Instance-reset does the same per app
  (`handler/instance.go:434`). Because a preview *is* an app, **preview teardown is exactly app
  teardown** — `compose down -v` + `DeleteApp` + a reconcile to prune routes. Reuse it.
- **Managed DBs link by `app_id` and inject a sealed connection string into `env_vars`.**
  `managed_databases.app_id` (FK CASCADE), connection string sealed and upserted as the app's
  `DATABASE_URL` env (`store/managed_dbs.go`, `store/env_vars.go:83`). This is the dangerous edge:
  if a preview *inherited* the parent's `DATABASE_URL`, it would read/write production data. The env
  inheritance policy (decision #4) exists to prevent exactly this.

## What already exists (don't rebuild)

- **The whole pipeline, slug-keyed and branch-aware**: clone→build→up→attach→health→route→running
  (`deploy/pipeline.go:147-448`), project name `vac-{slug}` (`pipeline.go:638`), branch from
  `app.GitBranch` (`pipeline.go:502,507`).
- **Webhook → deploy decision**: signature verify + `ExtractRef` + `ParseRef` + `MatchTriggers` +
  coalesce + `CreateDeployment` + `worker.Enqueue` (`handler/webhooks.go:130-222`). The preview
  trigger forks *inside* this handler.
- **deploy_triggers**: `(app_id, event, filter)` with glob filter (`store/deploy_triggers.go:20-26`,
  migration `00020`). The "which branches get previews" rule reuses this row shape (decision #3).
- **Derived auto-hosts + pruning**: `AutoSubdomain` (`proxy/hostname.go:22`), `AutoHosts`/`IsAutoHost`
  (`proxy/manager.go`), `pruneOrphans` (`manager.go:418-448`), `vac-auto-*` `@id` (`network.go:24`).
- **App teardown**: `ctrl.Down(project, true)` + cascading `DeleteApp` (`handler/apps.go:435`,
  `store/apps.go:209`).
- **Env inheritance source**: `ListEnvVarsForApp` with per-row `sensitive` flag (`store/env_vars.go:37`,
  deviation D9) — the flag is the natural seam for "copy non-sensitive to preview."
- **UI tab insertion seam**: `ui/src/routes/_app/apps/$appId.tsx:28-51` — `TABS` + conditionally
  spliced `MANAGED_TABS`; a "Previews" tab slots in the same way. Deploy list pattern in
  `features/app-detail/deploys-tab.tsx`; deployment type in `ui/src/types/api.ts`.

## Scope decisions (the important part)

1. **A preview is an app, not a new entity.** Add `apps.is_preview BOOLEAN`, `apps.parent_app_id`
   (nullable FK → apps, `ON DELETE CASCADE` so deleting the parent reaps all its previews), and reuse
   `git_branch` for the preview's branch. Derived slug `{parent-slug}-{branch-slug}` (branch
   slugified + truncated; the whole slug must stay ≤ 63 chars / a valid DNS label). **Why:** every
   downstream system (pipeline, router, teardown, UI list) already keys off "app + slug" — making a
   preview anything else means duplicating all of them. The parent owns the repo, build config, and
   webhook secret; the preview overrides only `git_branch` and inherits the rest by reference.
2. **Preview host = `{parent-slug}-{branch}.{base}` (a flat label), not a deeper subdomain.**
   Because the preview is a real app with slug `{parent}-{branch}`, `AutoSubdomain` *already* yields
   `{parent}-{branch}.{base}` for free — same depth as any app, so it sits under the operator's
   existing `*.{base}` wildcard with no new cert tier. **Rejected:** `{branch}.{parent}.{base}`
   (e.g. `feat-x.blog.vac.dev`) — prettier, but it's a *deeper* label that a single-level wildcard
   cert does **not** cover, forcing every operator into a wildcard-of-wildcards or per-host ACME on a
   nested name. The flat form keeps the `<200MB`/low-friction posture and reuses the rowless
   auto-host path with literally zero proxy change. Multi-service previews fall back to
   `{service}.{parent}-{branch}.{base}` exactly as today (and *do* need the deeper wildcard — note it,
   don't solve it here).
3. **The "preview on push" rule reuses `deploy_triggers`** with a new event
   `TriggerEventPreview = "preview"` and a `filter` glob over branch names (e.g. `!main` semantics:
   default is "any non-default branch"). **Why:** matching machinery (`MatchTriggers`) and storage
   already exist; this is one constant + one branch in the webhook handler. A preview trigger means
   "a push to a matching branch creates-or-redeploys a preview app," distinct from the existing
   push trigger which redeploys the *parent*.
4. **Env inheritance is non-sensitive-only, and previews NEVER inherit the parent's managed DB.**
   On preview create, copy the parent's `sensitive=false` env vars (the D9 flag is exactly this
   signal); **do not** copy `sensitive=true` values and **do not** copy the injected `DATABASE_URL`
   from a managed DB. **Why:** sharing a production database with an ephemeral branch stack is a
   data-corruption / data-leak footgun — a preview running migrations against prod is a disaster.
   Policy: previews get a clean slate for secrets and data. If a preview needs a DB, the operator
   provisions a throwaway managed DB *on the preview app* (it's a normal app — managed DBs link by
   `app_id` and tear down on `DeleteApp`). A later refinement could offer an explicit, opt-in
   "seed from parent" but the safe default is isolation.
5. **Hard concurrency + lifetime caps, enforced at create time.** A global
   `VAC_MAX_PREVIEWS` (default small, e.g. 5) and an auto-expiry `VAC_PREVIEW_TTL` (default e.g. 72h
   of no push). **Why:** the box has one finite RAM/disk budget; unbounded preview fan-out is the
   one thing single-box reality cannot absorb. When the cap is hit, refuse the new preview with a
   clear notification rather than silently OOMing the box and taking production down with it.
   Auto-expiry is a long-lived goroutine (mirror `certcheck`/`diskusage.Collector`) that tears down
   previews idle past the TTL.
6. **Teardown trigger is best-effort and idempotent.** Branch-delete (push with zero after-SHA) and
   PR-close (if PR payloads are parsed) trigger teardown; the TTL sweeper is the backstop for
   anything the webhook misses (a deleted branch with no webhook delivery still expires). Teardown =
   `ctrl.Down("vac-"+preview.Slug, true)` + `DeleteApp(preview)` + a `proxy.Reconcile` to prune the
   now-orphaned `vac-auto-*` routes — the exact app-delete path.

## Phase 1 — Data model + preview lifecycle service

- Migration (next number, currently `00067` is latest → `00068`): `apps.is_preview BOOLEAN NOT NULL
  DEFAULT FALSE`, `apps.parent_app_id UUID NULL REFERENCES apps(id) ON DELETE CASCADE`,
  `apps.last_preview_push_at TIMESTAMPTZ NULL` (for TTL). Partial index on `parent_app_id`.
- Store: `CreatePreviewApp(parent, branch)` (derives slug, copies non-sensitive env, seeds a
  `git_branch` override, leaves managed DBs unlinked), `ListPreviewsForApp(parentID)`,
  `CountActivePreviews()`, `ListExpiredPreviews(ttl)`. `DeleteApp` already cascades — teardown reuses
  it. The deploy `triggered_by` enum gains `"preview"` for provenance in the deploy list.
- New `api/internal/preview/` (thin): `EnsurePreview(parentID, branch)` (create-or-find + enqueue a
  deploy), `Teardown(previewID)` (down -v + DeleteApp + reconcile), and an `Expirer` long-lived
  goroutine wired in `main.go` next to `certChecker`/`diskusage`. The cap check lives here.

## Phase 2 — Webhook trigger

- Extend the webhook handler (`handler/webhooks.go:166+`): after `ParseRef`, if the branch is
  non-default and a `preview` trigger matches, call `preview.EnsurePreview(appID, branch)` instead of
  deploying the parent. Coalesce per preview-slug like the existing active-deploy guard.
- New teardown signals: detect a **branch-delete push** (`after` SHA all-zeros in the GitHub/GitLab
  payload) → `preview.Teardown`. Optionally parse `pull_request` `action:"closed"` (provider-specific;
  add behind the same handler, honest that it's new payload parsing). The TTL sweeper backstops both.

## Phase 3 — UI

- A **Previews** tab on the parent app (`$appId.tsx` splice seam, gated on the parent being a
  non-preview app): list each preview with branch, derived URL (link out), status (reuse deployment
  status pills), last-push time / TTL remaining, and a manual **Tear down** button (calls
  `preview.Teardown`). Mirror `deploys-tab.tsx` structure and the `Deployment` type extension.
- Surface the global preview count vs `VAC_MAX_PREVIEWS` somewhere honest (a small "3 / 5 previews"
  line) so the operator sees the budget. A preview app should be visibly badged in the apps list so
  it isn't mistaken for production.

## Out of scope (what single-box reality forbids)

- **Per-preview isolated infrastructure** — no dedicated VM/node/network-per-preview. Previews share
  the box, the kernel, and `vac-edge`; isolation is at the compose-project/volume level only. A
  hostile branch can still exhaust the shared box (the cap is the only backstop).
- **Unlimited / auto-scaling preview fan-out** — hard-capped by `VAC_MAX_PREVIEWS`. There is no
  "spin up 50 PR previews"; that's a multi-node PaaS, not VAC.
- **Inheriting the parent's production database** — explicitly forbidden (decision #4). No
  "previews share prod data," ever, by default.
- **Deeper-subdomain pretty URLs by default** (`{branch}.{app}.{base}`) — needs a nested wildcard the
  MVP doesn't mandate (decision #2). Flat `{app}-{branch}.{base}` only, for now.
- **Build-time preview caching / shared layers across previews** — each preview is a clean build.
  Layer caching is Docker's job, not a VAC feature here.
- **Preview-specific comment-back to the Git provider** (the "deploy preview ready ✅" PR comment) —
  that needs a Git API write token and provider-specific posting; defer. Notifications go through the
  existing Discord/Slack notify subsystem instead.

## Rough size

- Phase 1: 1 migration, ~4 store methods, 1 small `preview` package (ensure/teardown/expirer). Medium
  — the lifecycle service and the env-copy/no-DB policy are the real care points; the deploy itself is
  100% reused.
- Phase 2: ~1 constant, one fork in the webhook handler, branch-delete detection. Small-to-medium
  (PR-payload parsing is the variable cost).
- Phase 3: 1 tab + 1 query + type + teardown mutation + a badge. Small.

## Build order

1. Migration + store (`is_preview`, `parent_app_id`, `last_preview_push_at`, `CreatePreviewApp` with
   non-sensitive env copy and **no** managed-DB inheritance).
2. `preview.EnsurePreview` / `Teardown` reusing the existing deploy enqueue + app-delete paths; verify
   a manually-created preview app deploys, routes at `{parent}-{branch}.{base}`, and tears down clean
   (routes pruned on reconcile, volumes gone).
3. `preview` trigger event + webhook fork (create-or-redeploy on matching non-default branch push).
4. Caps (`VAC_MAX_PREVIEWS`) + `Expirer` goroutine (`VAC_PREVIEW_TTL`), wired in `main.go`; refusal
   notification through the notify subsystem.
5. Branch-delete teardown signal (+ optional PR-close parsing).
6. UI Previews tab + budget line + preview badge.
7. `/code-review` + `/simplify`; `/refresh-kb` (new `preview` package + webhook/event surface change →
   `architecture.md` + `deployment-flow.md`).

## Verification

- A push to a non-default branch on a repo whose parent app has a `preview` trigger creates a new
  `vac-{parent}-{branch}` stack, reachable over HTTPS at `{parent}-{branch}.{base}`, with the
  parent's non-sensitive env present and **no** `DATABASE_URL` from the parent's managed DB.
- A second push to the same branch redeploys the *same* preview (coalesced), not a duplicate.
- Deleting the branch (or hitting the TTL, or the manual Tear down button) runs `compose down -v`,
  deletes the preview app + its volumes, and the next reconcile prunes the `vac-auto-*` routes —
  `curl` to the preview host stops resolving to a route.
- Exceeding `VAC_MAX_PREVIEWS` refuses the new preview with a notification and leaves existing stacks
  (and production) untouched.
- Deploy failure on a preview never tears down the parent or other previews (the standing invariant).
- `make lint typecheck test` clean.
