# 08 — Accessibility (a11y) pass + authoring guidelines

**Goal:** Make the dashboard usable with a keyboard and a screen reader, respect
OS motion/contrast preferences, and put guardrails (lint + a checklist) in place
so new features stay accessible by default. Almost entirely `ui/` work; no
schema or API changes.

This plan has two halves:

- **Part A — Fixes:** concrete, file-referenced changes, ordered by impact.
- **Part B — Guidelines:** a reusable "how to build an accessible feature in
  VAC" checklist to fold into `docs/kb/conventions.md`.

Current state below was verified against source on branch `main` (commit
`26d21b1`). File:line references are accurate as of that commit — re-verify if
the touched files have moved.

---

## Where we stand today

**Strong foundation (keep doing this):**

- Interactive elements are real `<button>`/`<a>`, not `<div onClick>` — the audit
  found no click-handler-on-div anti-patterns.
- Radix UI primitives back every overlay (dialog, dropdown, popover, tooltip,
  tabs, select, switch), so focus trapping, `Escape`-to-close, and arrow-key
  navigation come for free.
- `focus-visible` rings are applied consistently (3px `ring-ring/50`) across
  button/input/textarea/select/switch/tabs.
- Semantic landmarks exist: `<main>` (`app-shell.tsx:14`), `<header>`
  (`topbar.tsx:40`), `<nav>` (`sidebar.tsx:30`, `breadcrumb.tsx:8`), real
  `<table>` semantics (`ui/table.tsx`).
- Icon-only close buttons carry `sr-only` text; decorative logo is `alt=""
  aria-hidden`; theme toggle is a proper `radiogroup`; the HTTP-insecure banner
  is `role="alert"`; sonner toasts get `aria-live` for free.
- Color OKLCH tokens look high-contrast and dark mode is wired to
  `prefers-color-scheme`.

**Gaps, ordered by impact** (detail in the work items below):

| # | Gap | Impact |
|---|-----|--------|
| 8.1 | No `eslint-plugin-jsx-a11y` and no axe in tests — nothing prevents regressions | High (process) |
| 8.2 | No skip-to-content link; active nav uses `data-status`, not `aria-current` | High |
| 8.3 | Log viewer (virtualized) has no `aria-live`/`role`; new lines aren't announced | High |
| 8.4 | Meters/progress/charts have no text/ARIA alternative | Medium |
| 8.5 | Form inputs not wired to errors (`aria-describedby`/`aria-invalid`/`required`); env-tab inputs are placeholder-only | Medium |
| 8.6 | No `prefers-reduced-motion` handling for ~40 animations/transitions | Medium |
| 8.7 | No dynamic `<title>` per route; `<nav>`s unlabeled; a few icon buttons unlabeled | Low |

---

## Part A — Fixes

### 8.1 Guardrails: a11y lint + a smoke axe test  *(do this first)*

**Now:** `ui/eslint.config.js` configures `@eslint/js`, `typescript-eslint`,
`eslint-plugin-react-hooks`, `eslint-plugin-react-refresh`, `eslint-config-prettier`
— **no `eslint-plugin-jsx-a11y`**. `ui/src/test/setup.ts` only imports
`@testing-library/jest-dom/vitest`; there is no axe-core anywhere.

**Change:**
- Add `eslint-plugin-jsx-a11y` and enable its `recommended` flat config in
  `ui/eslint.config.js`. Start at `recommended` (not `strict`); fix or
  explicitly `// eslint-disable-next-line` the initial findings so the baseline
  is clean and `make lint` stays green.
- Add `vitest-axe` (or `axe-core` + a small helper) and write one smoke test that
  renders the app shell + one representative page and asserts
  `expect(await axe(container)).toHaveNoViolations()`. This is a tripwire, not
  full coverage — it catches the gross regressions (missing labels, bad
  contrast in tokens, duplicate ids).

**Accept:** `make lint` runs jsx-a11y and passes; `make test` includes at least
one axe assertion; both fail loudly when an unlabeled icon button or a
`<div onClick>` is introduced.

### 8.2 Keyboard navigation: skip link + `aria-current`

**Now:**
- No skip-to-content link anywhere — keyboard/SR users tab through the whole
  sidebar on every page.
- `sidebar.tsx:36` marks the active route with `activeProps={{ 'data-status':
  'active' }}` (visual only). The app-detail sub-tabs do the same
  (`routes/_app/apps/$appId.tsx:85-96`). Screen readers never hear which item is
  current.
- `<nav>` elements are unlabeled (`sidebar.tsx:30`), so a SR landmark list shows
  two anonymous "navigation" regions (sidebar + breadcrumb).

**Change:**
- Add a visually-hidden-until-focused "Skip to content" link as the first
  focusable element in `app-shell.tsx`, targeting the `<main>` (give `<main>`
  `id="main"` and `tabIndex={-1}`).
- Set `aria-current="page"` on the active sidebar link and active app-detail tab.
  TanStack Router exposes active state — pass it through `activeProps` (e.g.
  `activeProps={{ 'data-status': 'active', 'aria-current': 'page' }}`), or read
  `isActive` from the link render prop.
- Label the nav regions: `<nav aria-label="Primary">` for the sidebar,
  (breadcrumb already has `aria-label="breadcrumb"`).

**Accept:** First `Tab` on any page focuses "Skip to content" and jumps to
`<main>`; the active nav item and tab expose `aria-current="page"`; landmark nav
regions have distinct accessible names.

### 8.3 Live log viewer announces updates

**Now:** `components/common/log-viewer.tsx` renders a `@tanstack/react-virtual`
list inside a plain scroll `<div>` — no `role`, no `aria-live`. New streamed
lines are invisible to SR users, and the "Jump to latest" button
(`log-viewer.tsx:129`) has no accessible name beyond its visible text (acceptable
but worth an explicit label).

**Change:**
- Wrap the log output region in `role="log"` with `aria-live="polite"` and
  `aria-label="Deployment logs"` / `"Runtime logs"`. Note: virtualization means
  only on-screen rows are in the DOM, so SR users still can't read scrolled-off
  history — accept that trade-off for the live tail, and rely on the existing
  log **export** (`log-panel.tsx:94-117`) as the "read everything" path. Document
  this limitation in the component.
- Keep the scroll container keyboard-focusable (`tabIndex={0}`) so arrow/PageDown
  scrolling works without a mouse.
- Add an explicit `aria-label` to the "Jump to latest" button.

**Accept:** With a screen reader, newly arriving log lines are announced (politely,
not spammed); the log pane is reachable and scrollable by keyboard.

### 8.4 Meters, progress bars, and charts get text equivalents

**Now:**
- `components/common/meter.tsx` and `ui/progress.tsx` are purely visual bars —
  no `role="progressbar"` / `aria-valuenow|min|max` / `aria-label`. The sidebar
  host vitals (`sidebar.tsx:104-114` `VitalRow`) show the number as text next to
  the meter (good) but the meter itself is silent and the threshold/alarm state
  (`meter.tsx` turns red >80%) is color-only.
- `features/app-detail/traffic-chart.tsx` (recharts) has no text alternative.

**Change:**
- Give `Meter`/`Progress` `role="progressbar"` + `aria-valuenow/min/max` and an
  `aria-label` (caller-supplied, e.g. `"CPU 42%"`). Where a meter has a danger
  threshold, add SR-only text ("CPU high") so the alarm isn't color-only.
- For the traffic chart, add an `aria-label` summarizing the series and a
  visually-hidden data table (or a "view as table" toggle) as the text
  alternative. Charts are flagged "deferred" in the improvements README — if
  charts stay deferred, at minimum give the existing chart container an
  `aria-label` and mark the SVG decorative.

**Accept:** Each meter/progress reports its value to a screen reader; danger
states are conveyed by more than color; the traffic chart has a text equivalent
or is explicitly labelled.

### 8.5 Forms: wire inputs to their labels, errors, and required state

**Now:**
- Inputs style `aria-invalid` (`ui/input.tsx:13`) but nothing sets it; error text
  (`login.tsx` `ErrorText`, `:142`/`:83`) is not linked to the field via
  `aria-describedby`. No `required`/`aria-required` on required fields.
- `features/app-detail/env-tab.tsx:343-356` env key/value inputs have **no
  labels at all** — placeholder-only. The row action buttons (reveal/lock/copy/
  delete, `:360-375`) use `title` but no `aria-label`. Reveal/hide toggles don't
  announce state.

**Change:**
- Establish the field pattern (see Part B): every input has an associated
  `<Label htmlFor>`; on error, set `aria-invalid` and point `aria-describedby` at
  the error node; mark required fields with `required`. Apply to `login.tsx`,
  TOTP/API-token dialogs, and notification settings.
- In `env-tab.tsx`, give each key/value input an accessible name (an `sr-only`
  `<Label>` or `aria-label` like `"Variable name, row 3"`). Convert icon action
  buttons to `aria-label`; make the reveal toggle `aria-pressed` so its state is
  announced. Announce validation failures via the error region, not only a toast.

**Accept:** Every form control has a programmatic label; invalid fields expose
`aria-invalid` + a described error; required fields are marked; the env editor is
fully operable and labelled with a screen reader.

### 8.6 Respect `prefers-reduced-motion`

**Now:** ~40 transition/animation utilities across the UI (meter width,
sheet/dropdown slide+fade+zoom, `animate-pulse` on `status-pill.tsx:63` and
skeletons, `animate-spin` loaders, the `@keyframes vac-pulse` at
`index.css:223-231`) with **no** `prefers-reduced-motion` guard.

**Change:** Add a global reduced-motion block in `index.css`:

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

Keep meaningful state changes instant rather than animated. For the looping
status pulse and spinners, prefer a static indicator when reduced motion is set.

**Accept:** With OS "reduce motion" on, the dashboard has no looping/large-motion
animation; overlays appear without slide/zoom.

### 8.7 Document title + remaining small labels

**Now:** `index.html:10` sets a single static `<title>`; no route updates it
(checked all routes). The user-menu trigger (`topbar.tsx:90-95`, initials only)
and the search trigger have no `aria-label`. `breadcrumb.tsx:52-62`
`BreadcrumbPage` carries a redundant `role="link"`/`aria-disabled="true"`
alongside the correct `aria-current="page"`.

**Change:**
- Set `document.title` per route (e.g. a small `useDocumentTitle` hook or
  TanStack Router `head`/`beforeLoad`), formatted `"<Page> — VAC"`.
- Add `aria-label` to the user-menu trigger ("Account menu") and search trigger
  ("Search — ⌘K"). Drop the redundant `role="link"`/`aria-disabled` from
  `BreadcrumbPage` (keep `aria-current="page"`).

**Accept:** Each route sets a distinct title; the user-menu and search triggers
have accessible names; breadcrumb current page has clean semantics.

---

## Part B — Authoring guidelines (fold into `docs/kb/conventions.md`)

A short, copy-pasteable checklist so new features ship accessible by default.
This is the durable deliverable — the fixes above are one-time; this keeps the
bar.

### The 10-point checklist for every new UI feature

1. **Use the primitive.** Reach for the existing Radix-backed component in
   `ui/src/components/ui/` (dialog, dropdown, popover, tooltip, tabs, select,
   switch) before hand-rolling. They bring focus trapping, `Escape`, and ARIA.
2. **Real elements for real actions.** Clickable → `<button type="button">`;
   navigation → `<a>`/`<Link>`. Never `<div onClick>`. (jsx-a11y enforces this.)
3. **Every interactive thing has an accessible name.** Icon-only button →
   `aria-label` *or* an `sr-only` span. Decorative icon/image → `aria-hidden` +
   `alt=""`.
4. **Every input has a label.** Associate with `<Label htmlFor>` (or an `sr-only`
   label). Placeholders are **not** labels. Required → `required`. Error → set
   `aria-invalid` and `aria-describedby={errorId}`.
5. **State is never color-only.** Status, validity, thresholds, active tabs must
   also be conveyed by text, icon, shape, or an ARIA attribute (`aria-current`,
   `aria-pressed`, `aria-invalid`, `role="status"`).
6. **Keyboard-operable, in order.** Tab reaches every control in a sensible
   order; nothing is mouse-only. Custom scroll regions get `tabIndex={0}`. Don't
   add positive `tabIndex`.
7. **Visible focus.** Use the shared `focus-visible:ring-*` pattern; don't strip
   outlines.
8. **Live updates announce.** Streaming/async regions use `role="status"` /
   `aria-live="polite"` (or `role="alert"` for errors). Don't over-announce —
   `polite`, scoped to the changing node.
9. **Landmarks & headings.** New pages render under `<main>`, start with one
   `<h1>` (via `PageHeader`), and don't skip heading levels. Set the document
   title.
10. **Honor preferences.** No motion that ignores `prefers-reduced-motion`; size
    in `rem` so browser zoom works; verify in light **and** dark themes.

### How to verify before calling it done

- `make lint` (jsx-a11y) and `make test` (axe smoke) pass.
- Tab through the feature with **no mouse** — reach and operate everything,
  focus is always visible, focus returns sanely after closing overlays.
- Spot-check one flow with a screen reader (VoiceOver `⌘F5` on macOS): labels
  read, state changes announce, no "button button" / unlabeled controls.
- Toggle OS "reduce motion" and confirm nothing loops or large-slides.

### Reference field pattern

```tsx
// Labelled input with error wiring — the canonical form control in VAC.
<div>
  <Label htmlFor={id}>Domain</Label>
  <Input
    id={id}
    required
    aria-invalid={!!error || undefined}
    aria-describedby={error ? `${id}-err` : undefined}
  />
  {error && <ErrorText id={`${id}-err`}>{error}</ErrorText>}
</div>
```

---

## Suggested order

1. **8.1 guardrails** — land lint + axe first so everything after is checked and
   regressions are caught.
2. **8.2 keyboard nav** and **8.6 reduced-motion** — cheap, global, high payoff.
3. **8.3 logs** and **8.5 forms** — the two highest-friction interactive areas.
4. **8.4 meters/charts** — depends partly on the deferred charts decision.
5. **8.7 titles + labels** — small polish.
6. Land **Part B** into `docs/kb/conventions.md` once the patterns above exist in
   the code to point at.

## Notes / trade-offs

- **Virtualized logs vs. SR completeness** (8.3): a live `aria-live` tail can only
  announce what's in the DOM; full history stays available via export. This is an
  accepted limitation, not a bug.
- **Charts** are marked deferred in the improvements README; 8.4 degrades
  gracefully to "label the container" if they stay deferred.
- No backend or schema changes anywhere in this plan.
