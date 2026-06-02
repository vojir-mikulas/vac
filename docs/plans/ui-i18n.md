# VAC — UI Internationalization (i18n) Plan

> Introduces translation infrastructure to the React SPA in [`ui/`](../../ui).
> **Scope: UI strings only.** Backend-generated text (API errors, notification
> payloads, crashloop reasons) stays opaque English for now.
> Scaffold ships **English-only**; later locales (in priority order): **Czech**,
> **German**, and possibly **Spanish**.

---

## 1. Goal & Scope

Make every user-facing string in the dashboard translatable, without changing any
visible behavior in the initial scaffold, and lay a pipeline so adding a locale is
"drop in a JSON folder + flip a switcher entry."

### In scope
- i18n runtime (`i18next` + `react-i18next`) wired into `main.tsx`.
- Translation catalogs under `ui/src/i18n/locales/`, English as source-of-truth.
- Namespacing that mirrors `ui/src/features/*` (lazy-loaded per feature).
- Language switcher in **Settings**, persisted client-side (localStorage), browser
  language auto-detect with `en` fallback.
- Locale-aware date/number formatting routed through the active language.
- Tooling: missing-key extraction, type-safe `t()` keys, an ESLint guard against new
  hardcoded strings, and a vitest i18n wrapper.

### Out of scope (for now)
- Backend / API string translation (error messages, Discord/Slack notifications,
  crashloop reasons) — treated as opaque English.
- Server-side persistence of the user's language preference (client-only first).
- RTL support (no RTL target language planned).
- Actually authoring cs/de/es catalogs — this plan delivers **en scaffold + pipeline**;
  the other locales land in follow-up PRs.

### Current state
`ui/` has **no** i18n today. ~114 `.tsx` files, ~150–200 user-facing strings, all
hardcoded English. Provider tree in `main.tsx`:
`QueryClientProvider → ThemeProvider → TooltipProvider → RouterProvider`.
Locale formatting is ad hoc (`toLocaleString`, `lib/format.ts`, and a hardcoded
`'en-US'` in `features/app-detail/traffic-chart.tsx`).

---

## 2. Library choice

**`i18next` + `react-i18next` + `i18next-browser-languagedetector`.**

- Mature, framework-agnostic, strong TS support, lazy-loaded namespaces, built-in
  pluralization / interpolation / `<Trans>` for rich text.
- Locale JSON is lazy-loaded, so only the active language ships to the browser — bundle
  cost stays low even as locales are added.
- Alternatives rejected: `@lingui` (adds a compile macro to the Vite build),
  `react-intl` (heavier, more boilerplate).

New deps: `i18next react-i18next i18next-browser-languagedetector`.

---

## 3. File layout

```
ui/src/i18n/
  index.ts                 # i18next init: resources, fallbackLng, detection, namespaces
  resources.ts             # generated glob of locale JSON (Vite import.meta.glob)
  react-i18next.d.ts       # type augmentation generated from en/ for t() autocomplete
  locales/
    en/                    # source of truth + fallback
      common.json          # buttons, generic labels, toasts, validation
      apps.json            # one namespace per feature folder
      app-detail.json
      deployments.json
      logs.json
      settings.json
      security.json
      addons.json
      database.json
      activity.json
      onboarding.json
    cs/ ...                # added in a later PR
    de/ ...                # added in a later PR
    es/ ...                # maybe, later
```

Namespaces mirror `ui/src/features/*` so each feature owns its strings and they load
with the route. `common` is always-loaded shared text.

---

## 4. Implementation phases

Each phase is an isolated, reviewable PR. Phases 3+ are independent and can interleave.

### Phase A — Infrastructure (no visible change)
1. Add deps.
2. `ui/src/i18n/index.ts`: init `i18next` with language detector
   (`localStorage` → `navigator`), `fallbackLng: 'en'`, namespaces, `en` resources.
3. Import the i18n singleton in `main.tsx`; wrap the tree in `<Suspense>` for lazy
   locale loads (place i18n init before `RouterProvider`, covering mock mode too).
4. Seed `en/common.json` and migrate `common`-level UI (shared buttons, toasts).
5. Add a **language switcher** to `features/settings/instance-section.tsx`
   (display only `en` until other locales exist).
6. Add the vitest i18n wrapper (init with `en` resources) so RTL `getByText` queries
   keep passing.

### Phase B — Tooling & guardrails
1. `i18next-parser` (or a small script) to extract keys and report missing/orphan keys;
   wire into `make lint` / CI.
2. Type-safe keys: generate `react-i18next.d.ts` from `en` JSON so bad keys fail
   `make typecheck`.
3. ESLint `i18next/no-literal-string` scoped to `features/`, initially **off** — flip to
   error per-folder as each feature is migrated.

### Phase C — Per-feature string migration (one PR per feature)
Order by visibility/volume: `apps → app-detail → deployments → logs → settings →
security → addons → database → activity → onboarding`.

Per feature: replace hardcoded JSX/attribute text with
`const { t } = useTranslation('<feature>')` → `t('key')`, populate `en/<feature>.json`,
flip that folder's lint rule to error.

Non-trivial cases to handle:
- Pluralization (container/deploy counts) → i18next plural keys.
- Interpolation (`{{count}} deploys`, names) → `t('k', { count })`.
- Rich text with embedded components → `<Trans>`.
- Attributes: `aria-label`, `title`, `placeholder`, `alt`.
- `useDocumentTitle` strings.
- `sonner` toast messages.
- `zod` / form validation messages.
- Locale-aware formatting: route `lib/format.ts`, `toLocaleString`, and the hardcoded
  `'en-US'` in `traffic-chart.tsx` through the active i18n language.

### Phase D — Add real locales (later)
1. **Czech** first (validates plurals — cs has a non-trivial plural rule set —
   interpolation, and date/number formatting end-to-end).
2. **German** (long compound words → check layout/truncation).
3. **Spanish** (maybe).

Each is: copy `en/` → `<lang>/`, translate, add to the switcher + supported-languages
list. The missing-key check (Phase B) gates completeness.

---

## 5. Decisions locked / open

**Locked**
- UI-only; backend text stays English.
- English scaffold first; client-side persistence; `en` fallback.
- Future locales: cs, de, then maybe es.

**Open (confirm before Phase D)**
- Czech plural handling reviewed by a native/fluent speaker (the `cs` plural rules are
  the main correctness risk).
- German layout overflow (sidebar nav, buttons, table headers) — may need Tailwind
  tweaks once `de` lands.

---

## 6. Acceptance (scaffold milestone = Phases A–B)

- `make build`, `make typecheck`, `make lint`, `make test` all green.
- App renders identically to today (English), now sourced from `en` catalogs.
- Switching language in Settings is wired (only `en` selectable until a second locale
  ships) and persists across reload.
- Adding a locale is a documented, mechanical drop-in folder + switcher entry.

---

## 7. Backend & notification translation (future — out of scope for the scaffold)

Backend-generated text splits into **two delivery paths** that need opposite strategies,
because one has a browser in the loop and one does not. Neither is built now; this
section records the design and the cheap prep worth doing early.

### Path 1 — API responses → translated in the UI, backend stays English

`WriteError` already emits `{"error": msg, "code": <derived>}`
([`api/internal/server/handler/json.go:43`](../../api/internal/server/handler/json.go)).
**The `code` is the translation key.**

- The UI keeps an `errors` namespace mapping `code → message` (e.g.
  `invalid_credentials → "Wrong code"`). It renders by `code`; `msg` is only a
  dev/fallback string.
- **Backend does zero i18n work** on this path — the browser knows the locale and does
  the translating. No Go i18n runtime, ever.
- **Prep (cheap, worth doing during migration):** dynamic errors must carry **structured
  params**, not values baked into `msg`. `"port 8080 in use"` can't be translated from a
  string — it needs `code: "port_in_use"` + `params: { port: 8080 }`, with the UI
  interpolating. Audit which error codes are dynamic and add a `params`/`details` field
  to those responses.

### Path 2 — Discord/Slack notifications → rendered server-side

Notifications are **fire-and-forget server push** (`notify` dispatcher): async, no
request, no browser, **no locale in scope**. The server must pick a language and render
the text itself.

`notify.Event`
([`api/internal/notify/events.go:30`](../../api/internal/notify/events.go)) is already a
**render-neutral struct** — stable `Type` (`deploy_succeeded`, `crash_loop`, …) plus
structured fields (`AppName`, `Commit`, `Duration`, `OK`). That is the right shape for
i18n. The blocker is that `Title`/`Message` are **pre-rendered English** baked in at the
event-creation sites.

To make it translatable:
1. **Stop pre-rendering.** Producers pass only `Type` + structured fields (drop or
   demote `Title`/`Message` to fallback). Rendering moves *down* into the
   dispatch/channel layer (`discord.go`/`slack.go`), which selects a template by `Type`
   and fills in the fields.
2. **Server-side catalog.** Embed locale files into the Go binary (`go:embed`) and render
   via a `EventType`→template map, or `golang.org/x/text/message` for proper plural rules.
   Keys align 1:1 with `notify.EventType`.
3. **Language source:** there is no per-request locale, so add **one instance-level
   "notification language" setting** (operator picks it in Settings; stored in DB/config).
   This fits VAC's one-operator/one-box model; per-channel language would be
   over-engineering.

### Single source of truth for keys

The UI is already embedded into the Go binary via `go:embed`, so the natural design is a
**single `locales/` tree** consumed by both: the UI imports the JSON at build, Go
`go:embed`s the same files for the notification namespace. To prevent drift, reuse the
missing-key check (Phase B) in CI to assert every backend notification key exists in
every locale.

### Sequencing

- **Now (scaffold):** backend stays English. The only worthwhile prep is ensuring error
  responses carry **stable codes + structured params**, so the UI can own Path 1 later
  with zero backend runtime.
- **When cs/de land:**
  - API errors → fully handled UI-side via the `code` map; no Go changes.
  - Notifications → a self-contained backend PR: add the instance notification-language
    setting, move rendering into the dispatch layer, embed the Go catalog. Independent of
    the UI feature-migration PRs.
