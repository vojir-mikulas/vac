# Port handling — two confirmed bugs

**Status:** triage · **Effort:** M · **Both confirmed in code**

## BUG 1 — changing a service's port + restart doesn't take effect

The container keeps running on the old port. Confirmed:
- `PatchAppService` (`server/handler/services.go:75`) → `SetServiceConfig`
  (`store/services.go:151`) does a DB `UPDATE` with `COALESCE` **and nothing else**.
- There is **no code path that redeploys / recreates the container** after a port change.
- A plain "restart" recreates the container from the *same* compose with the *same* port — the
  new `internal_port` in the DB is only consumed by Caddy routing, not by the container's
  compose definition.

**Fix:** after a port change, the service must actually be **redeployed** (regenerate the
effective compose with the new port and `up` it), not just restarted — and Caddy's upstream
must re-resolve to `{slug}--{service}:{new_internal_port}`. Decide which port is even
user-editable: `internal_port` is what Caddy dials over `vac-edge`; HTTP services don't publish
host ports (architecture invariant), so editing should target `internal_port` and trigger a
redeploy + route refresh. Surface "changing the port will redeploy this service" in the UI.

Refs: `store/services.go:14` (port fields), `deploy/pipeline.go:451` (port discovered from
`docker compose ps`), routing invariant in `CLAUDE.md`.

## BUG 2 — addon (Grafana) binds 3000, colliding with `vac-api`'s default 3000

- `vac-api` defaults to `Port: 3000` (`config/config.go:125`); the control-plane route also
  falls back to 3000 (`proxy/manager.go:461`).
- Addons get their port from the template's compose, not from any allocator
  (`addon/installer.go`), so a Grafana template on 3000 collides.

Note: HTTP services route by DNS alias over `vac-edge`, not host ports, so an *internal* 3000
inside the Grafana container is usually fine. The collision bites when something **publishes**
3000 on the host, or when the control port and an app port are conflated.

**Fix options:**
- Move `vac-api`'s default off the very common 3000 (e.g. 3000→ an uncommon control port) and/or
  make the Grafana addon template use its own internal port — so a fresh box never double-books
  3000. 3000 is too generic a default for the control plane to claim.
- If addons ever need a host-published port, add a small port allocator instead of hardcoding.

## Acceptance sketch

- Editing a service's port and saving **redeploys** it; the running container and Caddy both use
  the new port; UI warns it's a redeploy.
- A clean install + Grafana addon doesn't fight over 3000.
</content>
