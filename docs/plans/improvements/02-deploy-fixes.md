# 02 — Deploy correctness: compose detection, stuck state, logs, live preview

**Goal:** Make deploys trustworthy and observable. Backend (compose detection +
state) and UI (log scroll, live preview).

## 2.1 Compose detection: support `.yml` AND respect the configured path

**Now (bugs):**
- `api/internal/compose/detect.go` (`Detect`) only checks the literal
  `compose.yaml` then `docker-compose.yml`. It does **not** check `compose.yml`
  or `docker-compose.yaml`.
- `api/internal/deploy/pipeline.go:178` calls `compose.Detect(repoDir)` and
  **ignores `app.ComposeFile`** entirely. The per-app compose path (stored on
  `App.ComposeFile`, default `"compose.yaml"`, set in `store/apps.go` /
  `handler/apps.go`, persisted via migration `00004_apps.sql`) is collected from
  the user and never used.

**Change:**
- Add an explicit-path entry point: `DetectAt(repoDir, configuredPath string)`.
  If `configuredPath` is non-empty and not the default, resolve it relative to
  `repoDir`, `os.Stat` it, and use it directly (error clearly if missing:
  "configured compose file `X` not found in repo"). Guard against path escape
  (`filepath.Clean`, reject `..` / absolute outside repoDir).
- When no explicit path is configured, broaden auto-detection to the full set in
  priority order: `compose.yaml`, `compose.yml`, `docker-compose.yml`,
  `docker-compose.yaml`, then `Dockerfile` → generated wrapper, else
  `ErrNoComposeOrDockerfile`. Add `SourceComposeYML` / `SourceDockerComposeYAML`
  variants or keep `Source` coarse — caller only needs `Path`.
- Pipeline: pass `app.ComposeFile` into the detect call.
- Note interaction with **03 (adapters)**: once adapters land, the configured
  path / detection feeds the "compose" and "Dockerfile" adapters. Keep this
  change adapter-agnostic so it stands alone.

**Tests:** unit tests in `compose/detect_test.go` for each filename, configured
path (present/missing/escaping), and Dockerfile fallback.

**Accept:** A repo using `compose.yml` deploys; setting a custom compose path in
new-app/settings is honored end-to-end.

## 2.2 Failed deploy stuck in "queued" → must show error

**Now:** Backend transitions are correct — on any pipeline step error the defer
at `pipeline.go:145` calls `MarkDeploymentFinished(..., DeploymentStatusError)`,
and in-progress deploys are swept to `interrupted` at worker boot
(`store/deployments.go` `MarkInProgressDeploymentsInterrupted`, called from
`deploy/worker.go`). So a permanent `queued` in the UI is almost certainly a
**UI/live-update gap**, not a DB state.

**Investigate + fix (UI-first):**
- Confirm the deployments list / app-detail subscribe to deploy status updates
  (WS hub) and re-render on the `→ error` transition. Check the status→label
  mapping in `ui/src/components/common/status-pill.tsx` and the deploy steps in
  `ui/src/features/app-detail/deploy-steps.tsx` — ensure `error`/`interrupted`
  render as a terminal failed state, not folded into "queued".
- Verify the query invalidation / WS event actually flips the cached row;
  if the preview stream ends without a status refetch, add an invalidate on
  build-end (see 2.4).
- Backend safety net: if a deploy can sit in `queued` because the worker never
  picks it up (e.g. crash between enqueue and start), add a periodic reaper (not
  just boot-time) that marks rows stuck in non-terminal states past a timeout as
  `error` with a clear message. Confirm whether one already exists before adding.

**Accept:** A deploy that fails (or whose worker dies) shows `error` in the UI
within seconds, never a permanent spinner/queued.

## 2.3 Logs: can't scroll up — forced to bottom

**Now (bug):** `ui/src/components/common/log-viewer.tsx:44` runs
`virtualizer.scrollToIndex(last, {align:'end'})` in an effect keyed on
`lines.length` with **no check for the user's scroll position**. Every new line
yanks the viewport to the bottom, so scrolling up is impossible.

**Change:** Implement "stick to bottom only when pinned":
- Track an `atBottom` ref. On the scroll container's `scroll` event, compute
  `scrollHeight - scrollTop - clientHeight <= threshold` (e.g. 24px) → `atBottom`.
- In the new-lines effect, only auto-scroll when `atBottom` is true.
- When the user scrolls up (atBottom=false), show a small "Jump to latest ↓"
  affordance that re-pins on click.
- Keep the existing `autoScroll` prop as the initial/explicit override.

**Accept:** User can scroll up and stay there while logs stream; a control
returns to live tail.

## 2.4 Deploy log preview "infinitely spams errors" and pins to bottom

**Now:** The live deployment preview reuses the same `LogViewer`; the bottom-pin
is 2.3. The "infinite error spam" suggests the log/WS stream re-subscribes or
reconnects in a loop, or renders error lines repeatedly. Source:
`ui/src/lib/ws/use-log-stream.ts` (stream hook) + the deploy preview consumer
(`ui/src/features/app-detail/deploys-tab.tsx`).

**Investigate + fix:**
- Check the WS hook's effect deps / cleanup — a reconnect loop (effect re-running
  because a dependency identity changes each render) would spam connect/error
  lines. Stabilize deps (memoize URL/handlers), ensure single subscription per
  deployment, and back off reconnects.
- Ensure the stream **terminates** on `build-end` (the pipeline publishes
  `PublishBuildEnd`, `pipeline.go:136`) so the preview stops trying to read.
- De-dupe / cap error lines; don't render the same transport error every frame.
- Tie into 2.2: on build-end, invalidate the deployment query so status settles.

**Accept:** Live preview shows logs once, stops cleanly at end, no repeated error
flood, and (with 2.3) doesn't trap the scroll.

## 2.5 Ongoing deployment preview (missing feature)

**Now:** There's a live build-log stream and `DeploySteps`, but no consolidated
"a deploy is happening right now" surface. The design shows a live pipeline view.

**Change (after 2.3/2.4 land):**
- Surface an in-progress deploy prominently: on the app-detail header and/or
  apps list, show the active deploy with its current pipeline step
  (`cloning → building → deploying → health-checking`) using `DeploySteps`, the
  live `LogViewer` (pinned-tail), and elapsed time.
- Drive it off the existing deploy-status WS + build-log stream. No new backend
  if status transitions already publish (`deploy/status.go` states; verify each
  `setStatus` emits an event the UI can consume — add emission if missing).
- Reuse `StatTile`/`StatStrip` for elapsed/step counts if helpful.

**Accept:** Starting a deploy shows a live, self-updating preview through to
success/error without a manual refresh.

## Verification
`make test` (Go race + vitest) incl. new `compose` tests; `make typecheck`;
manual deploy of a repo with `compose.yml` and a deliberately-failing build to
confirm the error state + log behavior.
