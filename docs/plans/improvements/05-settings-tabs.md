# 05 — Settings: tabbed shell + Instance + Danger zone

**Goal:** Split the flat settings page into a tabbed shell (design
`design/project/src/view-settings.jsx`), fold existing settings in, and add the
two new tabs the operator asked for: **Instance** and **Danger zone**.

## Current state

- `ui/src/routes/_app/settings.tsx` is a single scrolling page with sections:
  Appearance (theme), Active sessions ("This device"), TOTP, API tokens,
  Notifications. Section components live in `ui/src/features/settings/`.
- Backends that already exist:
  - **API tokens:** `handler/api_tokens.go` (create/list/revoke), table
    `00003_api_tokens.sql`.
  - **Notifications:** `handler/notifications.go` (get/put/test), Discord+Slack
    webhooks encrypted, events JSONB (`00015_notification_settings.sql`).
  - **Sessions / TOTP:** `handler/sessions.go`, `handler/auth.go`.
- No backend for: instance version/update, instance-level danger-zone ops.

## Tab structure (only what we have/need — omit mock's Git/Backups/Team)

Left-nav tabbed shell (sticky aside + content, design lines 30–62), using the
shadcn `Tabs` primitive or a nav+route param. Tabs:

1. **Appearance** — theme control incl. the new System option (plan 01.6).
2. **Account & security** — sessions ("This device" badge fixed in 01.3) + TOTP.
3. **Notifications** — wire the existing UI to `handler/notifications.go`.
4. **API tokens** — wire to `handler/api_tokens.go`.
5. **Domains** — instance domain management → see **plan 06**.
6. **Instance** — new (below).
7. **Danger zone** — new (below).

Implementation: a `vac-settings-shell` two-column layout (nav + panel). Prefer a
route param (`/settings/$tab`) so tabs are linkable, or local state if simpler.
Reuse `SGroup`/`SRow`-style grouped cards (flat, border-based per plan 01.1).

## 6 — Instance tab

Content (operator-specified):
- **Version group:**
  - **Current** — control-plane image + worker version, e.g. `vac · 0.4.2`, with
    "Released <date>". Needs a backend read.
  - **Update channel** — segmented `stable | beta | edge`.
  - **Auto update** — toggle with hint "Pull and self-update during the
    maintenance window (Sun 04:00 UTC)."
- **Important:** auto-update does **not exist yet**. Render the channel selector
  and auto-update toggle as **disabled** controls (visually present, not
  interactive) with a "coming soon" affordance. Do not build a self-update
  mechanism.

**Backend (minimal, read-only):**
- Add `GET /api/instance/info` (new `handler/instance.go`) returning
  `{version, builtAt, channel}`. Source version from a build-time ldflags var
  (`-X main.version=...` in the Makefile/Docker build) or a constant; `builtAt`
  similarly. No write endpoints for channel/auto-update now.
- Wire the disabled UI to show the returned `version`/`builtAt`.

**Accept:** Instance tab shows real version/build date; channel + auto-update are
visible but disabled.

## 7 — Danger zone tab (instance-level)

Operator-specified operations (these affect the **whole VAC instance**, distinct
from the existing per-app start/stop/restart in `handler/stack_control.go`):

1. **Restart control plane** — "Restarts vac-api, vac-worker and vac-proxy. Apps
   keep running." → `Restart` button.
2. **Stop all applications** — "Stops every container managed by VAC." → button.
3. **Reset instance** — "Wipes apps, deployments, databases. Requires typed
   confirmation." → destructive button + typed-confirmation modal.

**Backend (new `handler/instance.go`, all gated to authenticated operator):**
- `POST /api/instance/restart-control-plane` — restart the control-plane
  containers. Mechanism: since vac-api can't cleanly restart itself, prefer
  triggering via the host (e.g. signal Docker to restart the `vac-*` containers
  through the docker socket the deploy path already uses, or rely on the
  container restart policy after a graceful exit). **Design the mechanism
  carefully** — document in `docs/deviations.md`. Apps on `vac-edge` must keep
  running (don't touch app containers).
- `POST /api/instance/stop-all-apps` — iterate managed apps and stop their
  stacks (reuse `stack_control` stop logic per app). Confirm before executing.
- `POST /api/instance/reset` — destructive: stop+remove all app stacks, wipe
  `apps`/`deployments`/per-app databases. **Require a typed-confirmation token in
  the request body** (e.g. body must echo the instance name / `RESET`); server
  re-validates. This is irreversible — log loudly, consider requiring re-auth.

**UI (Danger-zone section, design `SecDanger` lines 441–456):**
- `danger`-styled `SGroup` (err border/bg).
- Each row → button; Restart/Stop are confirm-on-click; **Reset** opens an
  `alert-dialog` (`ui/src/components/ui/alert-dialog.tsx`) requiring the operator
  to type a confirmation string before the destructive button enables.
- Surface success/failure via toast; for control-plane restart, show a
  "reconnecting…" state since the API may briefly drop.

**Accept:** Each danger op works behind appropriate confirmation; reset demands
typed confirmation both client- and server-side; restart keeps apps up.

## Verification
Go handler tests (auth-gated; reset rejects wrong confirmation); `make typecheck`;
manual: visit each tab, confirm existing notifications/tokens still work after the
move, exercise danger ops on a throwaway instance.

## Cross-refs
- Theme System option: plan **01.6**. "This device" badge: **01.3**.
- Domains tab content: plan **06**.
