# P3 — App-detail UX · detailed implementation plan

**Track:** [P3 in 00-parallel-tracks.md](00-parallel-tracks.md) · **Source notes:** [app-detail-ux.md](app-detail-ux.md)
**Status:** ready to implement · **Reconciled against source @ `27bf42d`**

This expands the four P3 items into concrete, sequenced work. Each item lists the backend
delta, the UI delta, the exact files, and an acceptance check. The track owns the **app-detail
UI**, the per-service **action** path in `server/handler/services.go` + `stack_control.go`, and
a **new exec/PTY endpoint**.

## Pre-flight: what already exists (so we don't rebuild it)

Reconciliation found most of P3 is wiring existing capability into the UI, not net-new backend:

- **Stage 0 is sufficient.** `appDTO` already carries `source`, `template_id`,
  `template_name`, `template_icon`, `git_url`, `git_branch`, `build_kind`, `mem_limit_mb`
  (`server/handler/apps.go:69-89`; mirrored in `ui/src/types/api.ts:21-43`). **No further DTO
  work is needed for P3.3** — see the overview data-sourcing table below.
- **App-level start/stop/restart already exist:** `POST /api/apps/{id}/{start|stop|restart}`
  (`handler/stack_control.go`, routes `server.go:311-316`). The UI already calls them from the
  Services tab via `useStackControl` (`services-tab.tsx:31-63`). **P3.1 is pure UI relocation.**
- **Per-service restart already exists:** `POST /api/apps/{id}/services/{name}/restart`
  (`RestartService`, route `server.go:316`) → `dockercli.Compose.Restart(ctx, project, service)`.
  **P3.2 adds Stop alongside it.**
- **Per-service log streaming already exists:** WS `GET /api/apps/{id}/services/{name}/logs`
  (`RuntimeLogsWS`, `handler/ws_logs.go:119`), consumed by `useRuntimeLogs(appId, service)`
  (`ui/src/lib/ws/use-log-stream.ts:95-118`) and `LogPanel`. **P3.2 "View logs" reuses this.**
- **`dockercli.Compose.Stop(ctx, project, service)` accepts a service arg** (`compose.go:90`)
  — so per-service stop needs only a thin handler, no new docker plumbing.
- **`dockercli.Compose.Exec` is non-interactive** (`compose.go:184`, no TTY/stdin) — **P3.4 is
  the only item needing real new backend.** WS lib is `coder/websocket`; audit infra is ready.

**Net:** P3.1 = UI only. P3.3 = UI only. P3.2 = one small handler + UI. P3.4 = new endpoint + dep.

---

## P3.1 — Header start / stop / restart-all  ·  **S**  ·  UI-only

Lift the stack controls from the Services tab into the app-detail header so they're reachable
from any tab (Docker-Desktop style).

**No backend change.** Reuses `useStackControl(appId)` (`ui/src/lib/api/apps.ts:64-74`).

**UI:**
- Add a control group to the header in `ui/src/routes/_app/apps/$appId.tsx` (header block
  ~lines 37-111), next to the existing "Deploy from HEAD" button (~lines 98-110).
- Extract the three-button cluster currently in `services-tab.tsx:31-63` into a shared
  `StackControls` component (e.g. `ui/src/features/app-detail/stack-controls.tsx`) and render it
  in **both** the header and the Services tab (avoid duplicated mutation logic).
- Gate button enable/disable on app `status` (e.g. disable Start when `running`, disable Stop
  when `stopped`, disable all while `building`). Show pending state from the mutation.
- Compact in the header (icon buttons + tooltips); keep labels in the tab.

**Acceptance:** Start/Stop/Restart appear in the header on every app-detail tab, fan out to the
existing app-level endpoints, reflect live status, and disable correctly mid-action.

**Risk:** none beyond layout. Header already branches on `source` (git vs addon) — keep the new
controls outside that branch so addon apps get them too.

---

## P3.2 — Per-service Stop + View logs  ·  **S**  ·  small handler + UI

Today each service row exposes only **Restart** + **Configure** (`service-card.tsx:23-91`). Add
**Stop** and **View logs**.

### Backend — add per-service Stop (and Start, for symmetry)

`dockercli` already supports it; only a handler + route are missing.

- **`server/handler/stack_control.go`** — add `StopService(s, docker, proxyMgr)` modeled on
  `RestartService` (same file). It should:
  - `LoadApp`, build project name `"vac-" + app.Slug`, call `ctrl.Stop(ctx, project, name)`.
  - Set that service's status to `ServiceStatusStopped` via `s.UpdateServiceStatus(...)`.
  - Re-sync Caddy: a stopped service must drop from upstreams — call the same `proxySync`/
    teardown helper the existing handlers use (mirror `RestartService`).
  - Emit audit (`audit.SetTarget("app", appID)`, `audit.Describe(... "stopped service web")`).
  - Consider also adding `StartService` so a stopped row can be brought back without a full
    stack Start — same shape, `ctrl.Start`, status `Running`, `proxySync`.
- **`server/handler/services.go` sync point:** P2.1 (port redeploy) already landed here at
  `27bf42d` (`PatchAppService` now re-syncs Caddy). Put the **lifecycle** handlers in
  `stack_control.go` next to `RestartService`, not in `services.go`, to keep the collision clean.
- **`server/server.go`** — register next to existing per-service route (`server.go:316`):
  ```go
  r.Post("/{id}/services/{name}/stop",  handler.StopService(s, docker, proxyMgr))
  r.Post("/{id}/services/{name}/start", handler.StartService(s, docker, proxyMgr))
  ```

### Frontend

- **`ui/src/lib/api/services.ts`** — add `servicesApi.stop(appId, name)` / `.start(appId, name)`
  and `useStopService(appId)` / `useStartService(appId)` hooks mirroring `useRestartService`.
- **`ui/src/features/app-detail/service-card.tsx`** — add a **Stop** button next to Restart
  (`size-3.5` icon, e.g. `Square`/`CircleStop`), enabled when the service is `running`; show a
  **Start** affordance when `stopped`. Reflect `restart`/`stop` pending state.
- **View logs** — add an action that routes to the Logs tab pre-scoped to this service. Two
  options; pick the cheaper:
  1. Navigate to `/apps/{appId}/logs?service={name}`, and have `logs-tab.tsx` read the
     `service` search param to set `LogPanel`'s initial service filter (the filter already
     exists at `log-panel.tsx:58-72`; the per-service WS path already exists).
  2. Inline expandable mini-log drawer on the card using `useRuntimeLogs(appId, name)`.
  → **Recommend option 1** (reuses the full log UI; one search-param wire-up).

**Acceptance:** each service row has Restart + Stop (+ Start when stopped) + View logs. Stop
stops only that container, marks it stopped, and removes it from Caddy upstreams; View logs lands
on the Logs tab filtered to that service. Actions are audit-logged.

**Risk:** ensure Stop's Caddy re-sync matches how `RestartService` keeps upstreams honest, or a
stopped service lingers as a dead upstream. Cover with the existing handler test pattern.

---

## P3.3 — Overview panel (Source + Stack)  ·  **M**  ·  UI-only

A right-side overview with two cards. **All fields are already available client-side** — no DTO
or store change needed. Sourcing:

| Card | Field | Source (already in UI) |
|---|---|---|
| **Source** | repo / git_url | `app.git_url` (`App` DTO) |
| | branch | `app.git_branch` |
| | commit (sha + msg) | latest deployment — `deployment.commit_sha` / `commit_message` (`api.ts:140-141`); already fetched by overview's "recent deployments" |
| | framework | `app.build_config.framework` (`BuildConfig.framework`, `api.ts:13`) |
| | *(addon apps)* | when `app.source === 'template'` → show "Installed from {`template_name`}" + `template_icon` instead of repo/branch/commit |
| **Stack** | build kind | `app.build_kind` (map to label, e.g. `dockerfile` → "Dockerfile (wrapped)") |
| | service count | `useServices(appId)` length |
| | RAM cap | `app.mem_limit_mb` (null → "unlimited") |
| | network | constant `vac-edge` (architecture invariant; not a per-app column) |

**UI:**
- New component `ui/src/features/app-detail/overview-panel.tsx` rendering the two cards.
- Place it as a right-side column on the Overview tab (`overview-tab.tsx` already has a right
  sidebar at lines 71-140 with Domains + recent deploys — add the panel there, or restructure so
  Source/Stack sit at the top of that column "first" as the note asks).
- Addon branch keyed on `app.source === 'template'` (matches the header's existing branch logic).
- `build_kind` → human label map (reuse any existing label map; otherwise add a small one).

**Acceptance:** Overview shows Source + Stack at a glance; git apps show repo/branch/commit/
framework; addon apps show "from addon" with the template name + icon; Stack shows kind, service
count, RAM cap, and `vac-edge`.

**Risk:** framework is only populated in `build_config` when detected (`adapter/framework.go`
currently only detects React) — render "—"/"auto-detected" gracefully when empty. Commit comes
from the latest deployment, so show "not deployed yet" when there are none.

---

## P3.4 — Container shell (WebSocket PTY)  ·  **L**  ·  new endpoint + dep · **privileged**

The biggest item and the only genuinely new backend. Open an interactive
`docker exec -it {container} sh` over a WebSocket PTY, rendered in xterm.js. This is a
**privileged action** (shell into a *user app* container, from the deliberately-sandboxed control
plane) — so it must be gated and audit-logged like env reveal.

### Approach (consistent with the existing `dockercli` CLI wrapper)

The current `dockercli.Exec` shells out to the `docker` binary but is non-interactive (no TTY, no
stdin). For an interactive PTY, attach a pseudo-terminal to `docker exec -i -t`:

- Add dep **`github.com/creack/pty`** (`moby/term` is already an indirect dep for raw-mode
  handling if needed). Alternative: use the already-present `moby/moby/client`
  `ContainerExecCreate` + `ContainerExecAttach{Tty:true}` and pipe the hijacked conn — cleaner but
  diverges from the CLI convention. **Recommend creack/pty + `docker exec`** to match `dockercli`.
- New `dockercli` method, e.g. `ExecInteractive(ctx, containerID string, cmd []string) (*ptySession, error)` that:
  - Runs `docker exec -i -t {containerID} {cmd...}` with a pty via `pty.Start`.
  - Returns the pty file (read/write) + a `Resize(rows, cols)` and `Close()`.
  - Default `cmd` = `sh` (fall back to `/bin/sh`); let the caller request `bash`.

### New WS endpoint

- **`server/handler/ws_exec.go`** — `ExecWS(s, docker)` handler:
  - Resolve app + service → `container_id` (services store already has `container_id`).
  - `ws.Accept` (same auth/origin path as `ws/conn.go` — session required, origin-checked).
  - Start the pty session; pump pty stdout → WS (binary/text frames), WS inbound → pty stdin.
  - Handle a small JSON control frame for **resize** (`{type:"resize",rows,cols}`) — xterm fit
    addon emits dimensions.
  - On WS close / app context cancel, kill the exec process and close the pty.
- **Route** (`server.go`, WS group with `RequireSession`, near the other WS routes ~320-322):
  ```go
  r.Get("/{id}/services/{name}/exec", handler.ExecWS(s, docker))
  ```

### Guardrails (non-negotiable — this crosses a trust boundary)

- **Gate behind explicit confirm** in the UI before opening (modal: "Open a root-capable shell
  into {service}?").
- **Audit-log the session** like env reveal: emit an audit entry on session open
  (`audit.SetTarget("app", appID)`, `audit.Describe("opened shell into service web")`,
  `audit.SetMetadata{service, container_id}`). Audit middleware only wraps mutating HTTP verbs —
  a WS GET won't be captured automatically, so **write the audit entry explicitly** from the
  handler (use `store.AppendAudit`/equivalent directly, mirroring how the audit store is called).
  Optionally log session close + duration.
- **Feature-flag it** (e.g. a config gate `VAC_ENABLE_SHELL`, default off) given it's the highest
  blast-radius feature — matches the "could graduate to its own upcoming/ stub" note.
- Document the boundary in `docs/deviations.md`: the control plane can already `docker exec` for
  backups; this exposes that capability interactively to the operator, behind confirm + audit.

### Frontend

- Add deps **`@xterm/xterm`** + **`@xterm/addon-fit`** (no terminal lib exists today).
- New `ui/src/features/app-detail/shell-tab.tsx` (or a per-service "Shell" action):
  - Mount xterm.js, connect to the exec WS via the existing `useWebSocket` base
    (`ui/src/lib/ws/use-websocket.ts`) or a thin dedicated hook (binary frames + resize control).
  - Wire fit addon → send resize control frames on mount/resize.
  - Confirm dialog before connect; show clear "session is audit-logged" notice; surface
    connect/disconnect state and a Reconnect button.
- Add a **Shell** tab (only when the feature flag is on) to the tab list
  (`$appId.tsx:22-35`), or a per-service "Shell" button in `service-card.tsx`.

**Acceptance:** operator can open an interactive shell into a running service container from the
UI, type commands and see output, the terminal resizes, the session is gated by a confirm and
recorded in the audit log, and the whole feature is off unless explicitly enabled.

**Risks / notes:**
- Stopped/crashed containers have no live `container_id` → disable Shell unless `running`.
- Long-lived WS: ensure the pty process is reaped on disconnect (no orphan `docker exec`).
- Binary vs text framing: shells emit raw bytes/ANSI; use `coder/websocket` binary messages and
  let xterm handle ANSI (don't JSON-wrap the stream; reserve JSON only for the resize control).

---

## Suggested sequencing

P3.1, P3.2, P3.3 are independent and small/medium — do them first in any order (they touch
disjoint UI files plus one small `stack_control.go` handler for P3.2). P3.4 is the large,
privileged item; do it last and consider promoting it to its own `upcoming/` stub if it grows.

```
P3.1 (header controls, UI)  ─┐
P3.2 (per-service stop+logs) ─┼─ parallel-safe ──► P3.4 (shell PTY, gated, new dep)
P3.3 (overview panel, UI)   ─┘
```

## Cross-track sync points that touch P3

- **`server/handler/services.go`** (sync #2): P2.1's port-redeploy already landed at `27bf42d`.
  P3.2's lifecycle handlers go in `stack_control.go` (next to `RestartService`), not `services.go`
  — no further collision.
- **App Settings UI** (sync #3): P3 doesn't touch Settings (P1.2 + P4.2 do) — no overlap.
- **Migrations:** P3 needs **none**.

## Commit plan (commitlint-compatible, one per item)

- `feat(app-detail): start/stop/restart-all in the app header`
- `feat(app-detail): per-service stop + view-logs actions`
- `feat(app-detail): overview panel with source & stack cards`
- `feat(app-detail): interactive container shell over WebSocket PTY` *(gated + audit-logged)*
