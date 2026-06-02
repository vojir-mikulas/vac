# Build cache, image prune & deployment retention

**Status:** triage · **Effort:** M

The note: *"does it solve caching for Docker? BuildKit layer cache + auto-prune image
retention + deployment retention."* Reality check, item by item:

## 1. BuildKit layer cache — already on

`DOCKER_BUILDKIT=1` is set on every build (`dockercli/compose.go:44`); the build is plain
`docker compose build` with no `--cache-from`, so it relies on the **daemon's local layer
cache**. That works for incremental rebuilds on the same box (which is VAC's model).

→ Optional improvement: add `--cache-from` / a BuildKit inline cache only if we ever build on
a different daemon or want cross-rebuild guarantees. Low priority for single-VPS.

## 2. Image prune / retention — built but NEVER CALLED (the real gap)

The plumbing exists and is dead code:
- `config.ImageKeepCount` default **3** (`config/config.go:43,136,245`).
- `dockercli/engine.go:91` `ListImages` — comment literally says *"Used by the image-prune step
  to keep only the most recent N per service"*.
- `dockercli/engine.go:110` `RemoveImage`.

**Nothing calls them.** So old per-service images accumulate forever. → Wire a prune step: after
a successful deploy (or on the retention pruner's tick), keep the most recent `ImageKeepCount`
images per service and `RemoveImage` the rest. The existing `internal/retention/pruner.go`
(`main.go:275`) is the natural home — it already prunes logs/metrics/audit on a schedule; add an
image-prune pass there. **M**

## 3. Deployment retention — does not exist

`internal/retention/pruner.go` prunes runtime logs, request metrics, and audit logs — **not
deployments**. There's no `DeleteDeployment`/old-deploy cleanup. → Add deployment retention
(keep last N deployments per app, or older-than-X), reusing the same pruner. Be careful to keep
enough history for rollback/revert (`internal/revert`). **M**

## Acceptance sketch

- After a deploy, only the newest `ImageKeepCount` images per service remain on disk.
- The retention pruner also trims old deployment rows beyond the rollback window.
- (Optional) document that BuildKit local cache is the caching story; no `--cache-from` needed
  on a single box.
</content>
