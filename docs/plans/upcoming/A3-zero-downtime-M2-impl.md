# A3 — Zero-downtime via M2 (VAC-managed generation containers) · Implementation plan

**Status:** ready to implement · **Supersedes:** the spike gate (step 0) of
`A3-zero-downtime-detail.md` · **Depends on:** A3 foundation (already landed, see below).

This plan commits to **mechanism M2** and drops the separate load-test spike. The reasoning:
the spike in the detail plan exists almost entirely to de-risk **M1**'s renormalization
question ("can `docker compose up --scale` settle back to one container cleanly for the next
deploy?"). M2 makes VAC own the rollable service's container name + lifecycle, so that question
disappears — "keep new, drop old" is a plain `docker rm`. The only remaining unknown is "does a
VAC-run container reproduce what compose would have run," which is **inspectable** (read it from
`docker compose config`) and **verifiable with a normal integration test**, not a throwaway
`hey` harness. So the spike folds into the real implementation + test.

---

## 0. What is already landed (the foundation — do not redo)

All mechanism-independent; behaviour-preserving by default (`ZeroDowntime` defaults OFF).

- **Classification** — `compose.Service.{HasVolumes,Replicas}` parsed from the service body;
  `rollable(internalPort, *compose.Service)` in `api/internal/deploy/rollable.go` (HTTP port +
  no volumes + single-replica). Unit-tested.
- **Route alias** — migration `00032` adds `services.route_alias`; `store.Service.RouteAlias` +
  `store.SetServiceRouteAlias(appID, name, *alias)`. `proxy.Manager.dial` honours it (so
  `routeFor` + `WaitHealthy` follow the live generation), falling back to the bare
  `{slug}--{service}` alias when empty. Non-rolling path unchanged.
- **Caddy cutover primitives** — `api/internal/proxy/rolling.go`:
  - `AttachGeneration(ctx, slug, service, gen, containerID)` — connect new container to vac-edge
    under `{slug}--{service}--{gen}`.
  - `GateGeneration(ctx, appID, service, gen)` — route carries **both** old + new upstreams,
    then waits for the new one healthy (old keeps serving the whole time).
  - `Cutover(ctx, appID, service, gen)` — atomic per-route narrow to the new upstream only.
  - `DetachContainer(ctx, containerID)` — detach a container from vac-edge.
  - Helpers `genAlias`, `genDial`; shared `waitForDials` extracted from `WaitHealthy`. Unit-tested.
- **Config** — `ZeroDowntime bool` (`VAC_ZERO_DOWNTIME`, default OFF) + `DrainWindow` (default
  10s, `VAC_DRAIN_WINDOW`) in `api/internal/config/config.go`. Unit-tested. **Not yet plumbed
  into `Pipeline`** — that happens in step 2 below.

---

## 1. The M2 mechanism, concretely

compose still owns **build** and all **non-rollable** services (`compose up -d` in place).
For each **rollable** service VAC runs the new generation itself:

```
docker run -d \
  --name vac-{slug}-{svc}-{gen} \
  --network {composeNet} \         # so it can reach the DB etc. by compose service name
  --env-file {repo}/.env \
  --label com.vac.app={slug} --label com.vac.gen={gen} \
  {image} [resolved command]
```

then `AttachGeneration` joins it to vac-edge under the generation alias. After the gate passes,
`Cutover` swaps Caddy, drain `DrainWindow`, `docker rm -f` the old generation (which also drops
its old vac-edge alias), and persist the new container id + `route_alias`.

**Deriving the run-spec without reimplementing compose.** Read it from compose itself rather
than guessing. `docker compose -p {project} config --format json` returns the fully-resolved
spec; for a rollable service pull only: `image`, `environment`/`env_file`, `command`,
`expose`/the internal port (already in `services.internal_port`), `healthcheck`, and the
networks. Rollable services are stateless+simple by definition (no `volumes:`, no `depends_on`
data deps), so the surface is thin. `image` for a built service is what compose tagged it —
`docker compose -p {project} images {svc}` or the `--format json` config's `image` field.

**`{composeNet}`** is compose's default network `{project}_default` (project = `vac-{slug}`).

### Two sharp edges (both already flagged in the detail plan §13) — handle explicitly

1. **`--remove-orphans` will kill the VAC-run container.** Today `dockercli.Compose.Up` runs
   `compose up -d --remove-orphans`. A VAC-run generation container joins the compose network
   but is *not* a compose service → compose treats it as an orphan → `--remove-orphans` removes
   it. **Fix:** when the app has any rollable service being VAC-managed, run the non-rollable
   `up` **without** `--remove-orphans` (or scope `up` to the explicit non-rollable service list).
   Decide: drop `--remove-orphans` globally for rolling deploys, or pass explicit service names.
2. **Compose must not recreate the rollable service.** Bring up only the non-rollable services:
   `compose up -d {nonRollable...}` (with `--no-deps` care so a rollable `depends_on` still gets
   its db up first — bring the dependency up explicitly). The rollable service is **never** a
   compose-up target once it's VAC-managed; VAC owns its container every deploy (not just during
   the overlap). This keeps the model uniform — no "first deploy uses compose, later deploys use
   docker run" split.

---

## 2. DockerClient extension

Extend `deploy.DockerClient` (and the real `dockercli.Compose`) with the generation operations.
Keep them on the same interface so pipeline tests can fake them.

```go
// New methods on deploy.DockerClient:
ConfigJSON(ctx, projectDir, composeFile, projectName string) ([]byte, error) // `compose config --format json`
RunGeneration(ctx, spec GenSpec) (containerID string, err error)             // `docker run -d ...`
InspectHealth(ctx, containerID string) (state string, err error)            // optional; Caddy gate is primary
RemoveContainer(ctx, containerID string, force bool) error                   // `docker rm -f`
```

`GenSpec` carries name, image, network, envFile, labels, command, and the internal port. Build
it from the resolved compose config + the persisted `services` row. Add a small
`compose`-package helper to extract a service's run-spec fields from the `config --format json`
output (unit-testable, no Docker).

Plumb `ZeroDowntime` + `DrainWindow` onto `deploy.Pipeline` (set in `main.go` from `cfg`).

## 3. Pipeline rolling branch (`Pipeline.Run`)

Insert after build + env render, replacing the single `p.Docker.Up(...)` call with a branch.
Keep the **non-rolling path byte-for-byte** today's behaviour.

```
classify each discovered/known service: rollable(internalPort, composeDef)
rollableOld := rollable services that have a live old container_id in the DB

if !p.ZeroDowntime || first deploy || len(rollableOld) == 0:
    p.Docker.Up(... as today, --remove-orphans ...)        # unchanged path
    ... existing ps → upsert → Sync → WaitHealthy ...
else:
    p.Docker.Up(non-rollable services, NO --remove-orphans)   # in place; deps first
    gen := shortID(deploymentID)
    for each svc in rollableOld (independent, sequential — smaller blast radius):
        spec  := buildGenSpec(configJSON, svc, gen)
        newID := p.Docker.RunGeneration(spec)
        p.Router.AttachGeneration(slug, svc, gen, newID)
        if err := p.Router.GateGeneration(appID, svc, gen); err != nil:
            p.Docker.RemoveContainer(newID, true)             # never cut over (§7 detail plan)
            p.Router.DetachContainer(newID)
            mark deploy error + app degraded; log; return nil # OLD KEEPS SERVING
        p.Router.Cutover(appID, svc, gen)
        log "draining old instance ({DrainWindow})"
        sleep DrainWindow (ctx-cancellable)
        oldID := svc.ContainerID
        p.Docker.RemoveContainer(oldID, true)                 # drops old alias too
        p.Store.SetServiceRouteAlias(appID, svc, genAlias(slug,svc,gen))
        p.Store.UpsertService(... container_id = newID ...)
    # first deploy of a rollable service: plain run + attach + gate (no old to overlap)
```

The `Router` interface (currently `AssignAutoDomains`/`Sync`/`WaitHealthy`) gains
`AttachGeneration`/`GateGeneration`/`Cutover`/`DetachContainer` — all already implemented on
`proxy.Manager`; just widen the interface in `deploy`.

## 4. Failure handling & invariants (detail plan §7 — preserve)

- New gen never healthy → **do not cut over**, remove the failed new container, mark deploy
  `error` + app `degraded`, return. The old container is untouched → **no downtime**. Recovery
  is **A1 rollback** (redeploy last-good).
- Deploy failure never tears down the running stack (repo invariant). The drain sleep is
  ctx-cancellable so the reaper's outer timeout still bounds a hung deploy.

## 5. Image retention (detail plan §13)

The old generation's image must survive the new build so a mid-cutover failure can fall back.
With M2 the old container pins its image by ID (Docker won't prune an in-use image), so the
immediate window is safe. Confirm the retention pruner keeps **active + last-good** rather than
"newest N by time," or a fast redeploy loop could prune the last-good image. Fold into this step.

## 6. UI (detail plan §10 — minimal v1)

Richer system-log lines already emitted by the pipeline branch: `starting new instance`,
`new instance healthy`, `cutover`, `draining old (Ns)`, `removed old instance`. Optional later:
a `cutover` deploy sub-status in `DeploySteps`, and a per-app "Zero-downtime deploys" toggle
(store on `apps`; gate the branch on it OR'd with the global `ZeroDowntime`).

## 7. Tests

- **Unit (no Docker):** `buildGenSpec` extraction from `compose config` JSON; branch-selection
  (non-rolling vs rolling vs first-deploy); drain-timing. `rollable()` + cutover primitives are
  already covered.
- **Integration (testcontainers + Docker) — this replaces the spike:**
  1. Redeploy a stateless HTTP service under a continuous request loop → assert **no non-200**
     across cutover.
  2. A **stateful** service (with a volume) is recreated in place, not rolled.
  3. A new generation that fails health leaves the old serving and marks the deploy `error`
     (no downtime).
  4. `--remove-orphans` on the non-rollable `up` does **not** remove the VAC-run generation.

## 8. Sequenced steps

| # | Step | Notes |
|---|---|---|
| 1 | `DockerClient` gen methods + `dockercli` impls; `buildGenSpec` from `compose config` JSON + unit test | no Docker for the extractor test |
| 2 | Plumb `ZeroDowntime`/`DrainWindow` onto `Pipeline`; widen `Router` iface with the 4 gen methods | wiring only |
| 3 | Pipeline rolling branch (§3) incl. the two `--remove-orphans` / no-recreate fixes (§1 edges) | core |
| 4 | Failure handling + drain (§4); retention "pin active + last-good" (§5) | |
| 5 | Integration tests (§7) — the spike-as-test | gates "ship it" |
| 6 | UI log lines (+ optional per-app toggle) (§6) | |
| 7 | `/code-review` + `/simplify`; flip `VAC_ZERO_DOWNTIME` default ON once §5 is green; regenerate `docs/kb/deployment-flow.md`; update `docs/deviations.md` (record M2 chosen, spike dropped) | last |

## 9. Open questions

- **`--remove-orphans` policy** for rolling deploys: drop it globally, or scope `up` to explicit
  non-rollable service names? (§1.1) — settle in step 3.
- **depends_on across a roll:** rollable web `depends_on` non-rollable db — bring db up first (in
  place) so the new web generation reaches it. Confirm in the step-5 integration test.
- **Multi-service cutover:** v1 rolls services independently + sequentially (simpler, smaller
  blast radius). Revisit only if a real stack needs coordinated cutover.
- **Per-app opt-out toggle:** global `ZeroDowntime` first; add the per-app column only if an
  operator needs to opt a finicky app out (§6).
