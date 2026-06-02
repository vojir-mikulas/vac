# P2 — Deploy & Build plumbing (detailed plan)

**Track:** P2 (see [`00-parallel-tracks.md`](00-parallel-tracks.md)) · **Status:** ready to build
**Owns:** `deploy/pipeline.go`, `internal/dockercli`, `internal/retention/pruner.go`, `config`,
the **port write-path** in `server/handler/services.go` + `store/services.go`, and the
control-plane port across `config` / `proxy/manager.go` / compose / install.
**Source plans:** [port-handling.md](port-handling.md), [build-cache-and-retention.md](build-cache-and-retention.md).

Sequence (shared files, so serialize): **P2.1 → P2.2 → P2.3 → P2.4**. P2.1 and P2.3/P2.4 both
touch the deploy/up + dockercli/pruner path; P2.2 is a near-isolated config sweep that can slot
anywhere but is cheapest right after P2.1 while the port code is fresh.

> **No migration needed.** Image prune and deployment retention are DELETE-only against existing
> tables; control-port is config/env. (`deployment_logs.deployment_id` is already
> `ON DELETE CASCADE`, `deployments.rolled_back_from` is `ON DELETE SET NULL` — see migration
> `00008` / `00020`, so deleting old deployment rows is FK-safe.) If any item grows a config
> *column*, claim `00060`+ per the track rules — but none of the items below should.

---

## P2.1 — Port change actually takes effect (BUG) · Effort M

### What's actually broken (read this before coding — the framing in triage is half-right)

The triage note says "the container keeps running on the old port" and prescribes a **redeploy**.
That's the wrong mental model for the field that's editable. Grounding in the code:

- `PatchAppService` (`server/handler/services.go:75`) → `SetServiceConfig`
  (`store/services.go:151`) does a DB `UPDATE … COALESCE` **and nothing else** — no proxy push,
  no container action. That's the bug: the write is invisible until the *next* deploy.
- `internal_port` is **not** a port VAC can make the container bind. It is purely "the port
  Caddy dials over `vac-edge`": `Manager.dial()` builds the upstream as
  `{slug}--{service}:{internal_port}` (`proxy/manager.go:322-324`, via `portOr(svc.InternalPort)`).
  The container listens on whatever the app/image listens on; VAC doesn't and can't rewrite that.
- So editing `internal_port` means **"tell the router the real port the app serves on."** The
  correct, minimal fix is **re-sync the Caddy route** (new dial port + health path) — *not* a
  container rebuild. A rebuild wouldn't change the listening port anyway.
- `health_path` (Caddy active-health path, `proxy/manager.go:296-311`) has the identical problem:
  edit is DB-only, never re-pushed.

So **BUG 1 is "the write never reaches Caddy,"** and the fix mirrors what `RestartService` /
`StartApp` already do: call `proxySync` after the DB write (`stack_control.go:30-37`).

### The second, subtler defect — operator overrides get clobbered on the next deploy

`UpsertService` (`store/services.go:49-61`) runs on every deploy and does:

```
internal_port = COALESCE(EXCLUDED.internal_port, services.internal_port)
```

`EXCLUDED.internal_port` is the port auto-detected from `docker compose ps`
(`pipeline.go:451`, `FirstTargetPort`). When detection returns a value it **overwrites the
operator's manual setting** — so a port the operator typed survives only until the next deploy
re-detects a (possibly different) target port. For a repo that only `expose`s a port with no
published mapping, detection may return 0 and the override sticks; behavior is inconsistent.

Decide the policy explicitly and document it next to `UpsertService`:
- **Recommended:** treat an operator-set `internal_port` as authoritative — don't let auto-detect
  clobber it. Either (a) skip the COALESCE overwrite when a manual value exists, or (b) add an
  `internal_port_source` notion. The cheapest correct version: in `upsertServices`
  (`pipeline.go:446`) pass `internalPtr = nil` when the existing row already has an
  operator-set port, so the existing COALESCE *preserves* it. This needs a "was it operator-set"
  signal — see "scope decision" below.
- **Minimum viable:** at least make the immediate effect correct (re-sync) and **document** that a
  redeploy re-detects the port. Don't ship the silent clobber without a comment.

### Scope decision (pick one, state it in the PR)

- **Slim (recommended for the bug fix):** `internal_port` + `health_path` edits re-sync Caddy
  immediately; no schema change; document the redeploy re-detect caveat. Closes the user-visible
  bug ("I changed the port and nothing happened").
- **Full:** add an `internal_port_overridden BOOLEAN` column (migration `00060`+) so the override
  survives redeploys deterministically. Only do this if the slim version's caveat is judged
  unacceptable. Heavier; coordinate the migration number with P6/P1.

`exposed_port` is "host-published port (Phase 2; diagnostics only now)" per `store/services.go:19`
— HTTP services publish no host ports (architecture invariant). Editing it changes nothing
routable today; either reject edits to it with a clear 400, or keep accepting it as a no-op
diagnostic field. **Don't** wire a redeploy off it.

### Implementation

1. **`server/handler/services.go` — `PatchAppService` signature + body.**
   - Add a `ProxyManager` param (the interface already exists at
     `handler/stack_control.go:25`: `Sync` + `Teardown`). New signature:
     `PatchAppService(s *store.Store, pm ProxyManager)`.
   - After a successful `SetServiceConfig`, if `req.InternalPort != nil || req.HealthPath != nil`,
     call `proxySync(r.Context(), pm, appID)` (reuse the existing helper — nil-safe, logs on
     failure). This re-pushes the route with the new dial port / health path.
   - Add an audit hook (`audit.SetTarget` + `audit.Describe`, e.g. "changed web port to 8080")
     so the change shows in the activity feed like the other mutating handlers.
2. **`store/services.go` — the clobber.** Implement the chosen policy (see above). If slim, add a
   comment on `UpsertService` documenting that auto-detect wins on redeploy and *why* that's
   acceptable; if full, thread the override flag through `upsertServices`.
3. **Route wiring (`server/server.go:276`).** Update the `PatchAppService(s)` call site to pass
   `proxyMgr` (already in scope there — it's the same `handler.ProxyManager` used by the lifecycle
   routes at `server.go:312-315`).
4. **UI copy** (`ui/src/features/app-detail/…` — the service settings field). Replace any
   "restart to apply" wording with the truth: **"Saving re-routes traffic to this port
   immediately."** If the full path is chosen, note the override persists across deploys.

### Tests

- Handler test: PATCH with `internal_port` → asserts `SetServiceConfig` called **and** the fake
  `ProxyManager.Sync` was invoked once for the app. PATCH with only an unrelated/no field → no
  sync. (Mirror the existing `stack_control` handler tests' fake-PM pattern.)
- Proxy already has `manager_test.go:131` asserting `dial` == `blog--web:3000`; add/confirm a case
  that a changed `InternalPort` yields the new dial string (cheap regression guard).
- If slim clobber-policy: a `store` test documenting that a redeploy with a detected port
  overwrites a manual one (so the behavior is pinned, not accidental).

### Acceptance

- Editing a service's `internal_port` (or `health_path`) and saving makes Caddy route to the new
  `{slug}--{service}:{port}` / health-check the new path **without a restart**; the change is
  audited; the UI states it re-routes immediately.

---

## P2.2 — Control plane off the over-claimed `:3000` (BUG) · Effort S

### Reality check

There is **no live routing collision** today: vac-api is off `vac-edge`, Grafana is reached by
DNS alias (`grafana` internal `expose: 3000`, *no host port* —
`addon/templates/grafana/compose.yaml:16`), so an internal 3000 inside Grafana never meets
vac-api's 3000. The real friction is two-fold and worth fixing for hygiene:

1. **Host-port collision in dev/self-host:** compose publishes `${VAC_HOST_PORT:-3000}:3000`
   (`compose.yaml:64`, `compose.prod.yaml:78`) and `install.sh` defaults `VAC_HOST_PORT=3000`
   (`scripts/install.sh:36`). 3000 is the single most common app dev port — `bench-ram.sh:36`
   already apologizes for it. A fresh box running anything else on 3000 fights vac-api.
2. **3000 is too generic a default for a control plane to claim**, conflating "the dashboard" with
   "every Node/Next/Vite app ever."

### The change — decouple and move the default off 3000

Keep the **internal** container port and the **host-published** port as separate knobs (they
already are: `VAC_PORT` vs `VAC_HOST_PORT`), and move the defaults to an uncommon port. Suggested
default: **`7000`-range or higher, deliberately uncommon** (e.g. `9393`; avoid macOS AirPlay's
5000/7000 and the common 8080/8000/3000/4000). Final pick is the operator's call — the
**invariant is that all of these move in lockstep:**

| File | Line | Today | Change |
|---|---|---|---|
| `config/config.go` | 125 | `Port: 3000` | new default control port |
| `proxy/manager.go` | 463 | `port = 3000` fallback | same default (must match config) |
| `compose.yaml` | 37, 64 | `VAC_PORT: "3000"`, `:3000` | new internal port; host map `${VAC_HOST_PORT:-NEW}:NEW` |
| `compose.prod.yaml` | 44, 78 | same | same |
| `scripts/install.sh` | 36 | `VAC_HOST_PORT:-3000` | new host default |
| `api/Dockerfile` | 75 | `EXPOSE 3000` | new internal port |
| `ui/vite.config.ts` | 29-30 | dev proxy `localhost:3000` | new port (dev API) |
| `scripts/bench-ram.sh` | 36 | comment | update note |

- The **proxy `ControlPort`** (`proxy.Manager` cfg, default-3000 fallback at `manager.go:461-464`)
  must equal `config.Server.Port` for the dashboard route to dial `vac-api:{port}` correctly.
  Verify where `ControlPort` is populated from config in `main.go`/server wiring and that the new
  default flows through; if the fallback constant and the config default ever diverge the
  dashboard route silently breaks. Update `manager_test.go:339,355` accordingly.
- **Grafana template needs no change** — internal `expose: 3000` over alias routing is fine and
  collides with nothing. (This is the [sync-point #4](00-parallel-tracks.md) with P1.3, but P2.2
  ends up *not* touching the template, so the only coordination is "P2.2 confirms the template is
  fine; P1.3 owns any template edits.") State this in the PR so P1.3 isn't blocked waiting.
- Keep everything **env-overridable** (already is via `VAC_PORT` / `VAC_HOST_PORT`); only defaults
  move. Note the change in the release notes / install docs since existing operators bookmarking
  `:3000` need to know.

### Tests / verify

- `config` test: `Default().Server.Port` == new value; `VAC_PORT` override still wins.
- `proxy` control-route test: dial string is `vac-api:{new}`.
- `make compose-up` smoke: dashboard reachable on the new host port; `/health` green.

### Acceptance

- A clean install plus the Grafana add-on never double-books a port; the dashboard's default host
  port is no longer the universally-contested 3000; `VAC_PORT`/`VAC_HOST_PORT` overrides still work.

---

## P2.3 — Image prune wiring · Effort M

### The gap

`ImageKeepCount` (default **3**, `config/config.go:43,136,243-247`), `ListImages`
(`dockercli/engine.go:91-108`), and `RemoveImage` (`dockercli/engine.go:110-119`) all exist and
are **never called** — per-service images accumulate on disk forever. `ListImages` filters by
`com.docker.compose.project` + `com.docker.compose.service` labels; `Image` carries
`{ID, Repository, Tag, CreatedAt}` (`dockercli/types.go:96-101`).

### Design — add an image-prune pass to the retention pruner

The pruner (`retention/pruner.go`) is the natural home (it already runs nightly and prunes
logs/metrics/audit). It currently has **no docker dependency** and a narrow `PruneStore`
interface, so this adds two seams:

1. **`retention.ImagePruner` interface** (new, satisfied by `*dockercli.Compose`):
   ```go
   type ImagePruner interface {
       ListImages(ctx context.Context, projectName, serviceName string) ([]dockercli.Image, error)
       RemoveImage(ctx context.Context, id string) error
   }
   ```
   To avoid `retention → dockercli` coupling on a concrete type, either import the `Image` type or
   define a tiny local `imageRef{ID, CreatedAt string}` and have the pruner depend on an interface
   returning that. Prefer importing `dockercli.Image` (already a leaf package) for simplicity.
2. **Enumerate (project, service) pairs.** Add to `PruneStore`:
   `ListApps(ctx) ([]store.App, error)` (exists on `*store.Store`) and reuse
   `ListServicesForApp`. The compose project is `composeProject(slug)` = `"vac-"+slug`
   (`pipeline.go:532`) — replicate that helper or expose it. For each app → each service →
   `ListImages(project, service)`.
3. **Prune logic** (new `pruneImages` method, called from `PruneOnce`):
   - For each (project, service): list images, **sort by `CreatedAt` desc** (parse the docker
     `CreatedAt` string — note format `2024-01-02 15:04:05 -0700 MST`; if parsing is fragile,
     `docker images` returns newest-first already, so document the reliance or add `--format` with
     a sortable field). Keep the newest `ImageKeepCount`, `RemoveImage` the rest.
   - **Ignore "image is in use" errors** — that's the currently-running image and the expected
     case (`RemoveImage`'s doc already calls this out). Log at debug, continue. Don't fail the
     whole prune pass on one removal error.
   - Log a summary: `{project, service, removed, kept}`.
4. **Config plumbing.** Pass `ImageKeepCount` into `retention.Config` (new field) from
   `main.go:275`. Default already exists in `config`; thread it through.
5. **Wiring (`main.go`).** `retention.New(...)` gains the docker client + keep-count. The pruner
   is constructed after `docker` is available (it is — `docker` exists well before line 275).

> Optional (not required): also call `pruneImages` once at the end of a **successful** deploy in
> `pipeline.go` (after `MarkDeploymentFinished(...Running)`) so disk is reclaimed promptly rather
> than waiting for 03:00. If added, share the same `pruneImages` routine; keep it best-effort
> (log-only on error) so it never fails a green deploy.

### Tests

- `pruner_test.go`: fake `ImagePruner` returning N>keep images for a (project, service); assert the
  oldest `N-keep` get `RemoveImage`'d and the newest `keep` don't. A fake that returns an
  "image in use" error on the survivor → assert the pass still completes and prunes the rest.
- Verify sort: feed unsorted `CreatedAt` values, assert correct survivors.

### Acceptance

- After deploys accumulate images, a pruner tick leaves only the newest `ImageKeepCount` (default
  3) per service; the in-use image is never removed; one removal failure doesn't abort the pass.

---

## P2.4 — Deployment retention · Effort M

### The gap

`retention/pruner.go` prunes runtime logs, request metrics, audit log, and ring-buffers — **never
deployments**. There is no `DeleteDeployment`/bulk cleanup, so the `deployments` table grows
unbounded (and with it `deployment_logs`, which is the permanent build-log store). `rolled_back_from`
is `ON DELETE SET NULL` and `deployment_logs` is `ON DELETE CASCADE`, so deleting old deployment
rows is FK-safe and reclaims their build logs.

### Must-preserve constraints (don't break rollback)

Rollback (`store/deployments.go:83` `CreateRollbackDeployment`) targets a prior deployment that is
`status='running'` **with a non-nil `commit_sha`**. The history UI lists up to 100
(`ListDeploymentsForApp:119-122`). So retention must:

- **Never delete a non-terminal deployment** (`queued/cloning/building/deploying/health-checking`)
  — those are in flight.
- **Always keep the most recent successful (`running`) deployment per app** — that's the live
  version / the obvious rollback target; deleting it would orphan the running stack's record.
- Keep **enough successful history** that the rollback UI has targets. A count-based
  "keep last N per app" with a sane default (e.g. **`DeploymentKeepCount = 20`**) is simplest and
  matches the existing count-style knobs (`ImageKeepCount`, `LogRingBuffer`). Time-based
  (`older than X days`) is an alternative but risks nuking all history for a rarely-deployed app —
  prefer count-based, optionally floored by "always keep the latest running".

### Design

1. **Store method** (`store/deployments.go`), e.g.:
   ```go
   // DeleteOldDeploymentsForApp keeps the most recent keepN deployments per app
   // (by triggered_at desc) plus always retains the latest `running` row and any
   // non-terminal row, deleting the rest. Returns rows deleted. CASCADE drops
   // their deployment_logs; rolled_back_from pointers SET NULL.
   func (s *Store) DeleteOldDeploymentsForApp(ctx context.Context, appID string, keepN int) (int64, error)
   ```
   SQL sketch (single statement, no app-loop round-trips if done per-app, or a window-function
   variant across all apps at once):
   ```sql
   DELETE FROM deployments d
   WHERE d.app_id = $1
     AND d.status NOT IN ('queued','cloning','building','deploying','health-checking')
     AND d.id NOT IN (
         SELECT id FROM deployments
         WHERE app_id = $1
         ORDER BY triggered_at DESC
         LIMIT $2                      -- keepN most recent
     )
     AND d.id <> (
         SELECT id FROM deployments
         WHERE app_id = $1 AND status = 'running'
         ORDER BY triggered_at DESC
         LIMIT 1                       -- always keep latest running
     );
   ```
   (Guard the `<>` against NULL when no running row exists.)
2. **Pruner pass.** Add `pruneDeployments` to `PruneOnce`: enumerate apps (`ListApps`, already
   added for P2.3) → `DeleteOldDeploymentsForApp(app.ID, keepN)`; sum + log deleted count.
3. **Config.** `retention.Config.DeploymentKeepCount` (new) + a `config.Config` field
   (`DeploymentKeepCount`, default 20) with a `VAC_DEPLOYMENT_KEEP_COUNT` env override mirroring
   the other count knobs (`config.go:243-247` pattern). Thread through `main.go:275`. **No DB
   migration** — config is env/yaml.

### Tests

- `store` integration test: seed >keepN deployments (mix of `running`/`error`/one in-flight) →
  assert oldest beyond the window are deleted, the in-flight row survives, the latest `running`
  survives even if it's older than keepN's cutoff, and `deployment_logs` for deleted rows are
  gone (CASCADE).
- `pruner_test.go`: fake store asserts `DeleteOldDeploymentsForApp` called per app with the
  configured keepN.

### Acceptance

- The pruner trims old deployment rows beyond the keep window; in-flight and latest-running
  deployments are never deleted; rollback still works for everything inside the window; build logs
  for pruned deployments are reclaimed via cascade.

---

## Cross-track sync points P2 touches

- **`server/handler/services.go`** ([#2](00-parallel-tracks.md)) — P2.1 edits the **port
  write-path** (`PatchAppService`); P3.2 adds **stop/logs actions** to the same file (different
  funcs). **Land P2.1 first** or coordinate the merge; the signature change to `PatchAppService`
  (adding `ProxyManager`) is the only churn that ripples to `server.go`.
- **Grafana addon template** ([#4](00-parallel-tracks.md)) — P2.2 concludes the template needs
  **no** change (internal `expose: 3000` is fine). P1.3 owns any template edits. P2.2 just says so
  in its PR so P1.3 isn't blocked.
- **Migrations** ([#5](00-parallel-tracks.md)) — P2 adds **none** in the slim path. Only the
  *optional* "full" P2.1 (`internal_port_overridden` column) would need `00060`+; coordinate the
  number with P6/P1 if taken.

## Suggested PR breakdown

1. `fix(deploy): re-route on service port/health-path change` — P2.1 slim.
2. `fix(config): move control plane off the default :3000` — P2.2 sweep.
3. `feat(retention): prune old per-service images` — P2.3.
4. `feat(retention): trim deployment history beyond the rollback window` — P2.4.

Each closes one acceptance block above; 1 and 2 are the confirmed bugs (highest value), 3 and 4
are disk-hygiene that can land any time after.
</content>
</invoke>
