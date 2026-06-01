# 01 — UI polish & interaction fixes

**Goal:** Remove the dated/broken visual rough edges. No schema or API changes
except where noted. Mostly `ui/` work.

## Items

### 1.1 Remove outdated card/button shadows
- **Now:** Mixed elevation. Sidebar card uses `shadow-sm`
  (`ui/src/components/layout/sidebar.tsx`); `brand` and `outline` button
  variants carry `shadow-xs` (`ui/src/components/ui/button.tsx`). The design
  reference uses one flat, near-shadowless token (`--shadow-card: 0 1px 2px
  rgba(...,0.04)`) — borders do the elevation work, not shadows.
- **Change:** Drop `shadow-xs` from `brand`/`outline` button variants. Drop
  `shadow-sm` from the sidebar card; rely on `border` + `bg-surface-1`. Keep
  shadows only on genuine overlays (dialog/dropdown/popover/sheet/tooltip), which
  is already where the design expects them. Audit for stray `shadow-*` on cards
  via grep and remove from non-overlay surfaces.
- **Accept:** No elevated drop-shadows on resting cards/buttons; overlays still
  cast shadow.

### 1.2 Cursor pointer on all interactive controls
- **Now:** `ui/src/components/ui/button.tsx` base has `disabled:pointer-events-none`
  but no `cursor-pointer`; Tailwind 4 / preflight no longer defaults buttons to
  pointer. Custom `<button>`s elsewhere vary.
- **Change:** Add `cursor-pointer` to the Button base classes (and
  `disabled:cursor-not-allowed`). Grep `ui/src` for bare `<button` and
  `role="button"` / clickable rows lacking a cursor and normalize. Prefer fixing
  the shared `Button` so most cases inherit it.
- **Accept:** Hovering any actionable control shows a pointer; disabled controls show not-allowed.

### 1.3 Fix the "This device" badge
- **Now:** `ui/src/features/settings/sessions-section.tsx` (~line 69) hand-rolls
  a `<span className="rounded bg-ok-bg ...">` instead of using the existing
  shadcn `Badge` (`ui/src/components/ui/badge.tsx`). It reads broken/inconsistent.
- **Change:** Replace the inline span with `<Badge variant="secondary">` (or a
  new `success`/`ok` variant if we want the green treatment — add it to the CVA
  variants in `badge.tsx`). Sweep other inline pill spans (e.g. status chips in
  `deployments-page.tsx`, domain SSL chips) and migrate the obvious ones to `Badge`.
- **Accept:** "This device" renders as a proper Badge; visual matches other badges.

### 1.4 Sidebar: floating card + host IP instead of server dropdown
- **Now:** `ui/src/components/layout/sidebar.tsx` pins to the viewport edges and
  has a `ServerSelector` button with a `ChevronDown` (single-host product — the
  dropdown is pointless).
- **Change:**
  - Make the sidebar a **floating card**: add padding on top/bottom/left so it
    detaches from the window edges (design uses `top-3` margin + rounded-xl;
    extend to a consistent inset gutter). Confirm the main content area's left
    offset still lines up.
  - Replace the dropdown: remove the chevron and dropdown affordance; show the
    host identity statically — status dot + hostname + **the VPS IP** (and "this
    host" tag). Source the IP from host metadata (see backend note).
- **Backend note:** If no field exposes the host IP today, add it to the host
  stats/info payload (`useHostStats()` source in `api/internal/.../metrics` →
  handler). Cheap: read from config or the request/host. Keep it read-only.
- **Accept:** Sidebar floats with breathing room; host row shows IP, no dropdown arrow.

### 1.5 Deployments table needs a header row
- **Now:** `ui/src/features/deployments/deployments-page.tsx` renders a
  flex/card list with no column headers.
- **Change:** Add a sticky header row (App · Commit · Status · Duration · When),
  or migrate to the shadcn `Table` (`ui/src/components/ui/table.tsx`) with a
  `TableHeader`. Keep the status pill and relative-time formatting.
- **Accept:** The deployments list has labeled columns.

### 1.6 System theme as a third option
- **Now:** `ui/src/components/theme/theme-provider.tsx` supports only
  `'light' | 'dark'`, persisted to `localStorage['vac.theme']`, toggled via
  `theme-toggle.tsx`. The pre-paint inline script in `index.html` reads the same.
- **Change:**
  - Extend `Theme` to `'light' | 'dark' | 'system'`. When `system`, derive the
    applied class from `matchMedia('(prefers-color-scheme: dark)')` and subscribe
    to changes. Default new installs to `system`.
  - Update the pre-paint script in `index.html` to resolve `system` before first
    paint (avoid flash).
  - Update `theme-toggle.tsx` to a 3-way control (Sun / Moon / Monitor icons) or
    a small segmented control; the design Appearance section can host it.
- **Accept:** Choosing System follows OS preference live; no FOUC; persists.

### 1.7 Copy SSH key button does nothing
- **Now:** `ui/src/components/common/copy-button.tsx` uses
  `navigator.clipboard.writeText` inside a `try/catch` whose catch is a silent
  no-op. In an insecure context (HTTP / non-localhost) `navigator.clipboard` is
  undefined, so the button silently fails — this is the reported bug on the
  new-app deploy-key card (`ui/src/features/app-detail/deploy-key-card.tsx`).
- **Change:**
  - Add a fallback path when `navigator.clipboard` is unavailable: a hidden
    `<textarea>` + `document.execCommand('copy')`, or select-the-text affordance.
  - On total failure, surface a toast (sonner) instead of swallowing — "Copy
    failed, select manually". Optionally show the failure state on the button.
  - Recommend documenting that production runs over HTTPS (where the native API
    works); the fallback covers dev/HTTP.
- **Accept:** Copy works in HTTPS prod and degrades gracefully (with feedback) on HTTP.

## Out of scope
Card content/layout redesigns; only the elevation/shadow treatment changes here.

## Verification
`make typecheck && make lint`; manual pass through apps list, deployments,
settings, new-app, sidebar in both themes + system.
