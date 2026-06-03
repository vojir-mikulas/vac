# VAC — Deployment Queue, Concurrency Control & Cancellation

> Adds operator-configurable deploy concurrency, a live "deployments queue"
> side-panel (running + queued, across all apps), and the ability to cancel a
> queued or in-flight deployment.
>
> **Why:** a burst of pushes (e.g. 10 repos at once) can today only ever run one
> deploy at a time (safe), but the operator has no visibility into what's waiting,
> no way to bump throughput on a beefier box, and no way to cancel a runaway build.

---

## 1. Goal & Scope

### In scope
- **Configurable global concurrency** — a `max_concurrent_deploys` instance setting
  (default `1`, preserving today's behavior; **capped at 8**), surfaced in Settings.
- **A real worker pool** — replace the single worker goroutine with N workers
  draining the existing queue channel.
- **Per-app serialization** — never run two deploys for the *same* app at once.
  **The existing coalescing rule is NOT enough** (see §1 verification); we enforce it
  with a DB-level guard. Concurrency only applies *across* apps.
- **Cancellation** — cancel a `queued` deploy (trivial) and an in-flight deploy
  (context-driven), using a **new `canceled` terminal status** (distinct from
  `interrupted`, which stays reserved for restart-mid-deploy).
- **Deployments queue side-panel** — a slide-over (right `Sheet`) reachable from the
  topbar/sidebar showing currently-running + queued deploys across all apps, with
  per-row cancel and live progress.
- **Live updates** — a deployments **WebSocket topic** so the panel reflects state
  changes without polling.

### Out of scope (for now)
- Per-app concurrency *limits* (we enforce exactly one-per-app, not a tunable).
- Live resizing of the pool without restart (startup-applied first; live-apply is a
  noted optional extension).
- Priority / reordering of the queue (FIFO only).
- Killing already-running app containers on cancel — we keep VAC's "deploy failure
  never tears down the running stack" invariant (`CLAUDE.md`).

### Current state (verified against source)
- **There is already a queue.** Triggers (webhook / manual / rollback) create a
  `queued` deployment row and call `worker.Enqueue(id)`, pushing onto a buffered
  channel (cap 32). A **single** goroutine drains it sequentially.
  - `api/internal/deploy/worker.go` — `Worker{ queue chan string; … }`, `Start`,
    `Enqueue` (non-blocking; returns `ErrQueueFull` → HTTP 503).
  - Concurrency is effectively **hardcoded to 1**. The code comment: *"One worker per
    process — concurrent deploys would thrash the build I/O on a typical VPS."*
- **Burst coalescing exists *only on the webhook path*.** `webhooks.go:188` checks
  `store.HasActiveDeployment()` and returns 202 "coalesced" if the app already has an
  in-flight build. **VERIFIED LEAKY** — see the verification box below.
- **The `queued` state already exists** and the `deployments` table *is* the
  persistent queue (rows in `queued`, `started_at IS NULL`).
- **Recovery already handled.** Boot sweep marks non-terminal rows `interrupted`
  (`MarkInProgressDeploymentsInterrupted`); a periodic reaper marks rows stuck
  >30 min as `error` (`ReapStuckDeployments`). Both are keyed off status, not worker
  count — **no changes needed** for N workers.
- **Context already plumbs to subprocesses.** `Pipeline.Run(ctx, id)` passes `ctx`
  into every long op — `git.Clone/Pull`, `docker.Build/Up`, `healthCheck` — and the
  CLIs use `exec.CommandContext`, so a cancelled context SIGKILLs the subprocess.
  **The gap:** the worker passes its *lifetime* context to every deploy; there is no
  per-deployment cancel func, so we can't interrupt *one* deploy today.
- **Settings singleton exists.** `instance_settings` table + `InstanceSettings`
  struct + GET/PUT handler pattern (see `base_domain`) — a clean home for the
  setting.
- **UI has the primitives.** `Sheet` (right slide-over), `StatusPill` (maps every
  deploy status → tone), `DeploySteps` (pipeline progress), the
  `notifications-section.tsx` form is a complete read/write settings pattern, and the
  WS client (`use-websocket.ts`) is a generic frame consumer.

**Net:** the architecture is ~80% there. The real work is (a) worker pool + setting,
(b) per-deploy cancel plumbing, (c) the side-panel UI + a WS topic, (d) a real
per-app guard (the existing coalescing can't be trusted — see below).

### ⚠️ Verification: the per-app guarantee is LEAKY (checked against source)

Read of all three trigger paths:

| Trigger | `HasActiveDeployment` guard | Result |
|---|---|---|
| Webhook (`webhooks.go:188`) | ✅ checks before `CreateDeployment` | guarded |
| Manual (`deployments.go` `TriggerDeployment`) | ❌ none — creates + enqueues unconditionally | **unguarded** |
| Rollback (`deployments.go` `RollbackDeployment`) | ❌ none — creates + enqueues unconditionally | **unguarded** |

Two ways two same-app deploys coexist:
1. A user clicks **Deploy**/**Rollback** while a webhook build is running → second
   `queued` row created with no check.
2. Even the webhook path is **TOCTOU-racy**: the `HasActiveDeployment` check
   (`webhooks.go:188`) and `CreateDeployment` (`webhooks.go:198`) aren't atomic — two
   simultaneous deliveries can both pass, then both insert.

Harmless **today** (1 worker ⇒ same-app rows run sequentially). **Breaks the moment
N>1**: two workers pick up two same-app rows and race the same git workdir + compose
stack. So a real guard is mandatory, not optional — see §2.2.

---

## 2. Backend — concurrency & the worker pool

### 2.1 Setting storage
- **Migration** (`api/internal/db/migrations/000NN_deploy_concurrency.sql`): add
  `max_concurrent_deploys INT NOT NULL DEFAULT 1` to `instance_settings`
  with `CHECK (max_concurrent_deploys BETWEEN 1 AND 8)` (**cap = 8**, resolved).
  The handler should clamp/validate to the same range before writing.
- **Struct**: add `MaxConcurrentDeploys int` to `store.InstanceSettings`.
- **Store methods**: `SetMaxConcurrentDeploys(ctx, n)` (upsert `ON CONFLICT (id)`,
  mirror `SetBaseDomain`); read via existing `GetInstanceSettings`.

### 2.2 Worker pool
File: `api/internal/deploy/worker.go`.
- Add `concurrency int` to `Worker` (passed via `NewPipelineWorker(pipeline, queueCap,
  concurrency)`; `main.go` reads the setting at startup and passes it).
- In `Start`, spawn `concurrency` reader goroutines instead of one, all selecting on
  the same `w.queue` / `ctx.Done()`. Go channels are safe for concurrent receivers,
  so the queue is the natural work distributor. `w.wg` already covers graceful
  shutdown — bump the `Add` count to match.
- **Per-app guard (REQUIRED — coalescing verified insufficient, see §1).** Chosen
  mechanism: a **DB-level partial unique index** enforcing at most one non-terminal
  deployment per app, which is atomic (kills the TOCTOU race) and makes manual +
  rollback consistent with the webhook's intent:
  ```sql
  CREATE UNIQUE INDEX one_active_deploy_per_app
    ON deployments (app_id)
    WHERE status IN ('queued','cloning','building','deploying','health-checking');
  ```
  - **Semantics:** this enforces *coalescing everywhere* — a manual/rollback/webhook
    trigger while the app already has an active-or-queued deploy is rejected at insert.
    All three handlers must catch the unique-violation (pg code `23505`) and translate
    it to a **409 "deploy already in progress / queued"** (the webhook keeps its 202
    "coalesced" response). This removes the now-redundant `HasActiveDeployment`
    pre-check race — the DB is the single source of truth.
  - With this index, the worker pool needs **no in-memory per-app lock**: two queued
    rows for one app can't exist, so two workers can never collide on an app.
  - **Trade-off / confirm at review:** this is *coalesce* semantics (second trigger
    rejected), not *queue-behind* (second trigger waits). Coalescing matches today's
    webhook behavior and is simpler/safer. If we later want queue-behind per app,
    switch to a keyed in-flight set (`map[appID]struct{}` + mutex, re-enqueue oldest
    queued row on finish) instead of the index. **Recommendation: ship the index
    (coalesce) now.**
  - **Migration ordering note:** add this index in the *same or later* migration as
    the new `canceled` status rollout; `canceled` is terminal so it's correctly
    excluded from the partial predicate above.

### 2.3 Startup wiring
`api/main.go` (~L195): read `GetInstanceSettings().MaxConcurrentDeploys`, pass to
`NewPipelineWorker`. Graceful shutdown (`cancel()` + `worker.Wait()`) already works
for N workers.

---

## 3. Backend — cancellation

### 3.1 Per-deployment context registry
File: `api/internal/deploy/worker.go`.
- Add `inflight map[string]context.CancelFunc` + `sync.Mutex` to `Worker`.
- In the worker loop, before `w.run`: derive `deployCtx, cancel := context.WithCancel(ctx)`,
  register under the deployment ID, `defer` both `cancel()` and map deletion. Pass
  `deployCtx` (not the lifetime `ctx`) to the pipeline.
- Expose `func (w *Worker) Cancel(id string) bool` — looks up & calls the cancel func,
  returns whether it was in-flight.

### 3.2 New `canceled` status (resolved)
File: `api/internal/deploy/status.go`:
- Add `DeploymentStatusCanceled = "canceled"` to the consts and to
  `IsTerminalDeploymentStatus`. Keep `interrupted` reserved for restart-mid-deploy so
  the timeline reads correctly (user cancel ≠ "vac-api restarted").
- `canceled` is terminal ⇒ correctly excluded from the §2.2 partial-unique predicate,
  so cancelling frees the app for a new deploy immediately.

### 3.3 Pipeline early-exit guard
File: `api/internal/deploy/pipeline.go` (after the row is loaded in `Run`):
- If the row is already terminal/`canceled` when picked up (i.e. a *queued* deploy was
  cancelled before a worker got to it), log a system line, mark finished, return `nil`
  without running steps.
- Map `context.Canceled` in the failure `defer` to a clean "deployment cancelled"
  message + status `canceled`, rather than a generic `error`.

### 3.4 Store
- For a queued deploy: `MarkDeploymentFinished(ctx, id, "canceled", "user cancelled")`
  is enough — the §3.3 guard prevents execution if it's already dequeued, and an
  as-yet-undequeued row simply gets skipped on pickup.

### 3.5 HTTP handler
File: `api/internal/server/handler/deployments.go`.
- `POST /api/apps/{id}/deployments/{did}/cancel`:
  1. Load deployment, verify it belongs to `{id}`.
  2. If already terminal → 422.
  3. Call `worker.Cancel(did)`. If it was in-flight, the context kill interrupts the
     subprocess and the pipeline's defer records the terminal status. If it was only
     queued (`worker.Cancel` returns false), directly `MarkDeploymentFinished(did, "canceled", ...)`.
  4. Return 200 `{ "status": "canceling" }`.
- Inject the worker (or a small `DeploymentCanceller` interface) into the handler
  group — wire in `main.go`/router.
- **Cleanup note:** on a mid-build cancel, app containers keep running the prior
  version (matches the "never tears down the running stack" invariant). Partial
  build artifacts are disposable (project dir re-pulled next deploy). Document this.

---

## 4. Backend — live deployments topic (WebSocket)

The panel needs running+queued across *all* apps, updating live. Today the UI polls
per-app lists every 3 s. Add a broadcast topic instead.

- Reuse the existing WS hub (the same infra behind `/api/deployments/{did}/logs` and
  runtime logs). Add an endpoint, e.g. `GET /api/deployments/queue/stream`, that:
  - on connect, sends a snapshot of all non-terminal deployments (+ enough context:
    app slug/name, commit, status, position);
  - pushes a frame whenever a deployment is created, changes status, or finishes.
- Emit these frames wherever status transitions already happen (the pipeline's
  `setStatus` / `MarkDeploymentFinished`, the enqueue path, and the cancel handler). A
  thin publish-on-transition hook keeps it DRY.
- **WS is the chosen path (resolved).** The panel subscribes to this topic for live
  state. A `GET /api/deployments/active` REST endpoint is still added for the initial
  snapshot / non-WS fallback (and is trivial given the store query below), but the
  steady-state UX is push-based, not polling.

New aggregate query in `store/deployments.go`: `ListActiveDeployments(ctx)` →
non-terminal rows joined to app name/slug, ordered by `triggered_at` (FIFO). Used both
for the WS connect-snapshot and the `/active` endpoint.

---

## 5. Frontend

### 5.1 API client + hooks (`ui/src/lib/api/`)
- `deployments.ts`: add `cancel(appId, did)` → `POST .../cancel`; add
  `listActive()` → `GET /api/deployments/active` (or the WS consumer).
- New `instance.ts` (or extend existing): `getDeploySettings()` /
  `updateDeploySettings({ max_concurrent_deploys })` following the
  `notifications.ts` pattern. Hooks: `useDeploySettings`, `useUpdateDeploySettings`,
  `useCancelDeployment`, `useActiveDeployments`.
- Add query keys to `lib/query/keys.ts`.

### 5.2 Settings — concurrency input
- New `features/settings/deployments-section.tsx` (number input + Save), mirroring
  `notifications-section.tsx` (load → local state → `mutate` → toast).
- New route `routes/_app/settings/deployments.tsx`; add a "Deployments" entry to the
  settings tab nav in `routes/_app/settings.tsx`. Copy: explain default 1, the cap,
  and that it applies across different apps (one-per-app always serialized).

### 5.3 Deployments queue side-panel
- New `features/deployments/queue-panel.tsx` using `Sheet` + `SheetContent
  side="right"`.
- Trigger: a button in the topbar (`app-shell.tsx`) or sidebar (`sidebar.tsx`) with a
  badge showing the active+queued count (driven by `useActiveDeployments` / WS).
- Contents: two groups — **Running** (with `DeploySteps` progress + elapsed) and
  **Queued** (FIFO order, position). Each row: app name, commit, `StatusPill`, and a
  **Cancel** button (`AlertDialog` confirm) calling `useCancelDeployment`.
- Live data: subscribe to the deployments WS topic (or `useActiveDeployments` poll).
  On cancel success, optimistic update + invalidate.
- Reuse `StatusPill`, `Button` (`variant="danger"`), `EmptyState` ("No active
  deployments").

### 5.4 Types
- Add `'canceled'` to `DeploymentStatus` in `ui/src/types/api.ts`, and map it in
  `StatusPill` (muted/err tone) + `lib/deploy-status.ts` classifiers.
- Active-deployment list item type (deployment + app slug/name).
- Handle the 409 "already in progress / queued" response from triggers in the trigger
  hooks (toast, not a hard error).

---

## 6. Decisions (resolved)

1. **`canceled` status** — ✅ add a distinct `canceled` terminal status (not reuse
   `interrupted`). User-initiated cancel reading as "interrupted" is confusing in the
   timeline. Touches status consts, `StatusPill`, and the §2.2 partial-index
   predicate (excluded — it's terminal). (§3.2)
2. **WebSocket topic** — ✅ ship the live WS deployments topic; `/active` REST stays
   as the connect-snapshot/fallback. Push-based steady state, no polling. (§4)
3. **Per-app guard** — ✅ **verified the coalescing guarantee is leaky** (manual +
   rollback unguarded, plus a webhook TOCTOU race; see §1 verification box). Enforce
   with a DB partial-unique index (coalesce semantics) — no in-memory per-app lock
   needed. (§2.2)
4. **Concurrency cap** — ✅ **8**. `CHECK (… BETWEEN 1 AND 8)`, default 1. Settings
   copy should note RAM/CPU guidance. (§2.1)

### Still open (low-stakes, decide at implementation)
- **Live pool resize** — startup-applied is the v1 (restart to change concurrency).
  Live-apply (drain & respawn workers on setting change) is an optional extension; not
  required for the original ask.
- **Settings copy** for the concurrency cap (the RAM/CPU guidance wording).

---

## 7. Suggested implementation order (incremental, each shippable)

1. **Per-app guard first** (§2.2) — the partial-unique index + translate `23505` to
   409 in all three trigger handlers, drop the now-redundant `HasActiveDeployment`
   pre-check. *This must land before N>1 or concurrent same-app deploys corrupt
   state.* Safe to ship while concurrency is still 1.
2. **Concurrency setting + worker pool** (§2.1/2.2) — migration, store, worker
   fan-out, startup wiring, settings UI (§5.2). Default 1 = no behavior change.
   *Solves the original "10 repos overwhelm the box" ask.*
3. **Cancellation** (§3) — `canceled` status, per-deploy context registry, pipeline
   guard, store, HTTP handler, `useCancelDeployment`.
4. **WS deployments topic + queue side-panel** (§4 + §5.3 + `ListActiveDeployments` +
   `/active` snapshot endpoint) — live visibility + cancel from one place.

---

## 8. Testing

- **Per-app guard:** test that a second trigger (manual/rollback/webhook) for an app
  with an active-or-queued deploy is rejected by the partial-unique index and mapped
  to 409/202 — on all three handlers. Include a concurrent-insert test to confirm the
  TOCTOU race is closed.
- **Worker pool:** unit test that N workers drain the queue concurrently up to N and
  that no two same-app deploys overlap (the index makes two same-app queued rows
  impossible). Race detector on (`make test` runs `-race`).
- **Cancellation:** integration test (testcontainers) — start a slow build, cancel
  mid-flight, assert terminal status + that the prior stack still serves; cancel a
  queued row, assert it never runs.
- **Recovery unchanged:** confirm boot sweep + reaper still behave with N workers.
- **UI:** vitest for the settings form + queue panel rendering/cancel flow.
- **Migration:** up/down + default-1 backfill.

---

## 9. Files touched (map)

**Backend**
- `api/internal/db/migrations/000NN_deploy_concurrency.sql` *(new)* — `max_concurrent_deploys`
  column (CHECK 1–8) **and** the `one_active_deploy_per_app` partial-unique index
- `api/internal/store/instance_settings.go` — struct field + setter (clamp 1–8)
- `api/internal/store/deployments.go` — `ListActiveDeployments`; translate `23505`
  (unique violation) to a sentinel the handlers map to 409; drop reliance on
  `HasActiveDeployment` pre-check
- `api/internal/server/handler/webhooks.go` + `deployments.go` — handle `23505` →
  409/202 on all three trigger paths
- `api/internal/deploy/worker.go` — pool (N readers), per-deploy ctx registry, `Cancel`
- `api/internal/deploy/pipeline.go` — early-exit guard, `context.Canceled` → `canceled`
- `api/internal/deploy/status.go` — `canceled` const + `IsTerminalDeploymentStatus`
- `api/internal/server/handler/deployments.go` — cancel handler, `/active` endpoint
- `api/internal/server/handler/instance.go` — deploy-settings GET/PUT
- `api/main.go` — read setting, pass concurrency, inject canceller
- WS hub wiring + publish-on-transition for the deployments topic (§4)

**Frontend**
- `ui/src/lib/api/deployments.ts`, `ui/src/lib/api/instance.ts`
- `ui/src/lib/query/keys.ts`
- `ui/src/features/settings/deployments-section.tsx` *(new)*
- `ui/src/routes/_app/settings/deployments.tsx` *(new)* + `settings.tsx` nav
- `ui/src/features/deployments/queue-panel.tsx` *(new)*
- `ui/src/components/layout/app-shell.tsx` or `sidebar.tsx` — trigger + badge
- `ui/src/types/api.ts` — status + active-list types
