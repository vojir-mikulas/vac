# Plan — Build caching

<!-- planning doc, drafted against commit f547245 on 2026-06-15 — intent, not current truth -->

Status: **proposed** (not yet implemented).

## Problem

VAC clones a user repo and builds it with:

```
docker compose --progress plain -p vac-{slug} -f {composeFile} build   # DOCKER_BUILDKIT=1
```

constructed in `Compose.Build` (`api/internal/dockercli/compose.go:37`), called from the
deploy pipeline (`api/internal/deploy/pipeline.go:342`).

There are **no explicit cache flags** — no `--cache-from`/`--cache-to`, no BuildKit cache
mounts, no cache config. Builds reuse only whatever layer cache the local Docker daemon
happens to retain, and that cache is fragile: the nightly retention pruner
(`api/internal/retention/pruner.go:177`) actively deletes images, nothing bounds cache
growth, and Docker's own GC can evict it between deploys. Caching is best-effort today and
silently degrades.

## Constraints that shape the design

- **VAC builds arbitrary _user_ compose files.** It does not own the Dockerfiles, so it
  cannot inject `RUN --mount=type=cache` mounts, and cannot assume a single Dockerfile or a
  single service.
- **`docker compose build` does not expose `--cache-from`/`--cache-to` as CLI flags.** Those
  live in the compose file's `build.cache_from`/`build.cache_to` keys (the user's repo). VAC
  can only inject them via an additive `-f` override — the same pattern already used for RAM
  limits at `api/internal/deploy/pipeline.go:357`.
- **Single VPS, single operator, disk-constrained, `<200 MB` idle RAM.** Cross-host cache
  portability (registry/`gha` backends) buys nothing here and costs disk/network. Local
  cache export (`type=local`, `mode=max`) duplicates layers onto the same disk — a net
  negative unless eviction is actually observed.

**Conclusion:** the highest-leverage, lowest-risk move is to make the _local daemon cache
reliable and bounded_, not to add cache export.

## Phase 1 — Reliable, bounded local cache (recommended, ship first)

Low risk, ~zero new RAM/disk, no user-repo changes.

1. **Config** (`api/internal/config/config.go`) — two new vars following the existing idiom
   (struct field near `ImageKeepCount`, a default in `Default()`, an `applyEnv` reader):
   - `VAC_BUILD_CACHE` (bool, default `true`) — master toggle / escape hatch; off = today's
     behavior.
   - `VAC_BUILD_CACHE_MAX_GB` (int, default `5`) — the cap the GC enforces.
2. **New dockercli method** `BuildCachePrune(ctx, maxBytes int64) error`
   (`api/internal/dockercli/engine.go`, next to `RemoveImage`) running:
   ```
   docker buildx prune --force --keep-storage {maxBytes}
   ```
   with a `docker builder prune -f --keep-storage` fallback for older CLIs. Works against the
   default builder — no custom builder instance required. Best-effort, same error mapping as
   `RemoveImage`.
3. **Wire into the existing nightly pruner** — add `BuildCacheMaxBytes` to `retention.Config`
   (`api/internal/retention/pruner.go:38`), extend the pruner interface (`pruner.go:31`), and
   add a best-effort cache-prune pass at the end of `PruneOnce` (`pruner.go:169`, after
   `pruneImages`). Wire the config value in `api/main.go:302`. This keeps cache GC and image
   GC under one 03:00 pass and keeps the idle-RAM budget untouched (subprocess, not a
   resident goroutine).

**Result:** builds reuse the daemon cache across deploys, the cache is bounded to
`VAC_BUILD_CACHE_MAX_GB`, footprint unchanged.

## Phase 2 — Persistent buildx builder + local cache export (only if eviction is observed)

Heavier; pursue only if real-world GC is seen wiping the cache between deploys.

1. **Create a persistent `docker-container` buildx builder once at startup** — `EnsureBuilder`
   (idempotent: `docker buildx inspect vac || docker buildx create --name vac --driver
   docker-container --bootstrap`), called near the `dockercli.New` wiring (`api/main.go:152`).
   Cost: a buildkitd container (~tens of MB RAM **only while building**, idle-stoppable). This
   is the one step that touches the RAM budget — hence the gating.
2. **Inject cache via an additive compose override `-f` file** (mirrors the RAM override at
   `api/internal/deploy/pipeline.go:357`). VAC parses resolved `Config()` output
   (`pipeline.go:283`) to learn which services have a `build:` section, writes an override
   setting `build.cache_from: [type=local,src=…]` and
   `build.cache_to: [type=local,dest=…,mode=max]` pointing at a VAC-owned cache dir under
   `WorkDir`, and runs the build through `--builder vac` (added in `Compose.Build`).
3. **Hard cache-dir size cap** — `type=local` + `mode=max` grows unbounded and
   `buildx prune --keep-storage` does not manage an external dir, so add directory-size
   enforcement (delete oldest / `rm -rf` + rebuild when over cap). This is the main reason
   Phase 2 is heavier and gated.

## Out of scope

- `gha` and `registry` cache backends — no benefit on a single-VPS single-operator box.
- BuildKit `RUN --mount=type=cache` mounts — require editing user Dockerfiles, which VAC
  categorically will not do.
- Switching the build engine from `docker compose build` to `docker buildx bake` — large
  rewrite of the build/stream/preflight path; not justified.
- Surfacing cache disk usage in the dashboard UI.

## Files to touch (Phase 1)

| File | Change |
|------|--------|
| `api/internal/config/config.go` | `BuildCache` + `BuildCacheMaxGB` fields, defaults, env readers |
| `api/internal/dockercli/engine.go` | `BuildCachePrune(ctx, maxBytes)` |
| `api/internal/retention/pruner.go` | `BuildCacheMaxBytes` in `Config` + interface method + prune pass in `PruneOnce` |
| `api/main.go` | pass new config into `retention.Config{}` |

Phase 2 additionally touches `api/internal/dockercli/compose.go` (`--builder vac`),
`api/internal/dockercli/engine.go` (`EnsureBuilder`), and `api/internal/deploy/pipeline.go`
(cache override file).

## Risks / trade-offs

- **Phase 1 is low-risk.** Main risk is `buildx prune --keep-storage` flag behavior differing
  across CLI versions — mitigated by the `builder prune` fallback and the pruner's existing
  best-effort error swallowing (`pruner.go:174`).
- **Image retention vs cache GC are independent.** Removing an old tagged image does not free
  cached layers a newer build still references — correct and desirable. Reason about total
  disk as roughly `IMAGE_KEEP_COUNT images/service + up to BUILD_CACHE_MAX_GB cache`.
- **Phase 2 RAM cost** — the `docker-container` builder runs buildkitd; on a `<200 MB` idle
  box this is the one thing that can breach budget _during builds_.
- **Phase 2 override fragility** — injecting `build.cache_from/cache_to` depends on parsing
  resolved compose and on each service actually having a `build:` section (image-only
  services must be skipped); version-sensitive, though the additive `-f` pattern limits blast
  radius.
