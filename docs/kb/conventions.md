<!-- generated from commit 0f94e36 on 2026-05-31 — regenerate with /refresh-kb; if HEAD has moved past this commit and api/internal/ or ui/src/ layout changed, treat as possibly stale -->

# Conventions — how the code is organized & how to add a feature

What the linters can't tell you. Style itself is enforced by golangci-lint, eslint, and
prettier (`make lint`) — don't restate it; this covers structure and patterns.

## Backend (`api/internal/`)

- **One package per concern.** A new subsystem gets its own package, not a grab-bag util file.
- **HTTP handlers** live in `server/handler/`, one file per resource (`apps.go`,
  `deployments.go`, `domains.go`, …). Handlers are thin: parse/validate → call a store or a
  subsystem → write JSON. Shared helpers: `json.go` (responses), `validate.go` (input checks).
- **Persistence** lives only in `store/`, one file per aggregate. Everything goes through pgx;
  schema changes are goose migrations. No SQL outside `store/`.
- **Status/state** enums and the app/service/deployment status derivation live in
  `deploy/status.go` — reuse them, don't re-define status strings.
- **Secrets** never touch the DB in plaintext — seal with `crypto.Box` (the same path as env
  vars / SSH keys / TOTP / webhook URLs), redact on read.
- **Real-time** goes over the `ws` hub by topic (`build:{id}`, runtime-logs per app,
  `stats:{appId}`, `host`). Producers publish to a topic; the hub fans out to subscribers.
- **Long-running watchers** subscribe to the single `dockerevents` bus rather than opening
  their own `docker events` stream.
- **Tests** sit beside code as `*_test.go`. Integration tests (real Postgres / Docker via
  testcontainers) are behind the `integration` build tag and run with `make test-integration`.

## Frontend (`ui/src/`)

- **Feature-foldered.** New dashboard area ⇒ new folder under `features/`. Keep its components,
  hooks, and queries together in that folder.
- **Server state** is TanStack Query; the typed API client and query setup live in `lib/api/`
  and `lib/query/`. WebSocket subscriptions go through `lib/ws/`. Don't hand-roll fetches.
- **Routing** is TanStack Router, file-based. `routeTree.gen.ts` is generated — never edit it
  by hand; add a route file and let the generator update it.
- **UI kit** is the shadcn/Radix-based components in `components/` + Tailwind 4. Reuse them
  before adding new primitives.

## Accessibility (a11y) — build it in by default

`eslint-plugin-jsx-a11y` runs in `make lint` and an axe smoke test
(`ui/src/test/a11y.test.tsx`) runs in `make test` — both fail loudly on gross
regressions (unlabeled controls, `<div onClick>`, duplicate ids, heading skips).
They're a tripwire, not full coverage. The checklist keeps the bar:

1. **Use the primitive.** Reach for the Radix-backed component in `components/ui/`
   (dialog, dropdown, popover, tooltip, tabs, select, switch) before hand-rolling —
   they bring focus trapping, `Escape`, and ARIA for free.
2. **Real elements for real actions.** Clickable → `<button type="button">`;
   navigation → `<a>`/`<Link>`. Never `<div onClick>`.
3. **Every interactive thing has an accessible name.** Icon-only button →
   `aria-label` *or* an `sr-only` span. Decorative icon/image → `aria-hidden` + `alt=""`.
4. **Every input has a label.** Associate with `<Label htmlFor>` (or `aria-label` /
   an `sr-only` label). Placeholders are **not** labels. Required → `required`. Error →
   set `aria-invalid` and `aria-describedby={errorId}` (see the field pattern below).
5. **State is never color-only.** Status, validity, thresholds, active tabs must also be
   conveyed by text, icon, or an ARIA attribute (`aria-current`, `aria-pressed`,
   `aria-invalid`, `role="status"`). E.g. `Meter` carries the over-threshold "high"
   state in `aria-valuetext`.
6. **Keyboard-operable, in order.** Tab reaches every control sensibly; nothing is
   mouse-only. Custom scroll regions get `tabIndex={0}`. Never use a positive `tabIndex`.
7. **Visible focus.** Use the shared `focus-visible:ring-*` pattern; don't strip outlines.
8. **Live updates announce.** Streaming/async regions use `role="status"` /
   `aria-live="polite"` (or `role="alert"` for errors). Scope it to the changing node;
   don't over-announce. (The log viewer's live tail is `role="log"` — see its note on the
   virtualization trade-off.)
9. **Landmarks, headings & title.** New pages render under `<main>`, start with one
   `<h1>` (via `PageHeader`) and don't skip levels (`SectionHeader` is `<h2>`). Name each
   nav landmark (`aria-label`). Set the document title via `useDocumentTitle`.
10. **Honor preferences.** Motion respects `prefers-reduced-motion` (handled globally in
    `index.css`); size in `rem` so zoom works; verify in light **and** dark themes.

**Verify before done:** `make lint` + `make test` pass; Tab through the feature with no
mouse (reach/operate everything, focus visible, focus returns after closing overlays);
spot-check one flow with VoiceOver (`⌘F5`); toggle OS "reduce motion" and confirm nothing
loops or large-slides.

**Reference field pattern** — the canonical labelled control with error wiring:

```tsx
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

## Adding a feature end-to-end (typical path)

1. **Migration** in `store/` (goose) if there's new persisted state.
2. **Store methods** for the new reads/writes (with a `*_test.go`).
3. **Subsystem logic** in its own `internal/` package if it's more than CRUD (a watcher, a
   pipeline step, an integration).
4. **Handler** in `server/handler/`, wired into the chi router in `server/`. Thin: validate →
   call store/subsystem → JSON. Add the auth middleware it needs.
5. **Real-time** (optional): publish to a `ws` topic if the UI needs live updates.
6. **UI**: a `features/` folder — query/mutation in `lib/api/`, components, a route file.
7. **Verify**: `make lint && make test && make typecheck`; run `make dev` to see it work.
8. **Review**: run `/code-review` (correctness) and `/simplify` (cleanup) before calling it
   done.
9. **Docs**: if the change alters architecture, the deploy pipeline, or these conventions,
   regenerate the affected `docs/kb/` file (`/refresh-kb`) so its provenance header matches the
   new commit. If it departs from `docs/plans/mvp.md`, add a row to `docs/deviations.md`.

## Commits

Conventional Commits, commitlint-enforced (`commitlint.config.js`). At the end of a
milestone/phase, propose a ready-to-paste commit message.
