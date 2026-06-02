# P4 — Domains (detailed plan)

**Track:** P4 (see [`00-parallel-tracks.md`](00-parallel-tracks.md)) · **Status:** ready to build
**Owns:** the base-domain path in `server/handler/instance.go` (`GetBaseDomain`/`PutBaseDomain`)
and `config/config.go`, the Settings → Domains UI (`ui/src/features/settings/domains-section.tsx`),
and a **new per-app Domains section** in `ui/src/features/app-detail/settings-tab.tsx`.
**Source plan:** [domains.md](domains.md).

Sequence (mostly independent, but share the base-domain client + settings screens, so serialize):
**P4.1 → P4.2**. P4.1 is a small honesty fix to one card; P4.2 is a net-new section that reuses the
existing per-app domain API + components. Both are additive — no behavior of the live router
changes.

> **No migration needed.** P4.1 adds a *computed* `BaseDomainSource` to `config.Config` (derived in
> `Load()`, not an env/yaml column or a DB column). P4.2 is pure UI over endpoints that already
> exist (`ListAppDomains`/`AddCustomDomain`/`DeleteAppDomain`). If anything here grows a DB column,
> claim `00060`+ per the track rules — but nothing below should.

---

## Reality check (read before coding — most of this already works)

The triage report ("base domain isn't showing the value that's set, probably can't override it,
requires a vac restart") is **stale on two of three counts**, confirmed against source:

- **Setting the base domain from the UI already exists** and is wired end-to-end:
  `BaseDomainCard` (`domains-section.tsx:47`) → `useSetBaseDomain` (`lib/api/instance.ts:62`) →
  `PutBaseDomain` (`handler/instance.go:122`).
- **It does NOT require a vac-api restart.** `PutBaseDomain` persists, calls `pm.SetBaseDomain` on
  the live proxy, and kicks a detached 30s reconcile that regenerates every app's derived auto
  routes and prunes orphans (`instance.go:148-163`). Caddy updates dynamically.
- **Per-app custom domains already have a full API:** `ListAppDomains` / `AddCustomDomain` /
  `DeleteAppDomain` (`handler/domains.go:88,132,175`), routed at
  `server.go:280-282` (`/apps/{id}/domains`, `/apps/{id}/services/{name}/domains`,
  `/apps/{id}/domains/{domainId}`), with TanStack hooks already written
  (`useDomains`/`useCreateDomain`/`useDeleteDomain`, `lib/api/domains.ts:41-63`).

So there is **no backend gap and no router bug**. What's real is (P4.1) a *display* defect — a base
domain set via `VAC_BASE_DOMAIN`/YAML renders the card **empty**, so the operator can't tell it's
configured — and (P4.2) a *surfacing* gap — per-app domains are manageable only from global
Settings, never from the app's own Settings.

---

## P4.1 — Base-domain card shows the effective value + its source (BUG) · Effort S

### What's actually broken

Base domain resolves with precedence **DB override → `VAC_BASE_DOMAIN` env → YAML → empty**
(`config/config.go:55,151,305-307`; the env/yaml merge collapses into the single `cfg.BaseDomain`
at load). `GetBaseDomain` (`handler/instance.go:95`) already returns both the raw override
(`base_domain`) **and** the resolved `effective`:

```go
effective := settings.BaseDomain
if effective == "" { effective = cfg.BaseDomain }   // instance.go:102-105
```

The bug is entirely in the **card**, which keys off the *override*, not the *effective* value
(`domains-section.tsx`):

- `const current = value ?? data?.base_domain ?? ''` (`:53`) — when the base domain comes from env
  or YAML, `base_domain` (the override) is `""`, so the **input renders empty** with placeholder
  `example.com`. The operator sees a blank field and concludes "it isn't showing the domain that's
  set." `effective` is computed at `:54` but used only inside helper copy, never surfaced as the
  current value.
- `data?.base_domain ? <WildcardSuggestion …/> : null` (`:107`) — the wildcard tip is also gated on
  the *override*, so it never shows for an env/YAML-configured base domain even though a wildcard
  cert is exactly as relevant there.

There is **no precedence bug** server-side — `effective` is correct. The fix is to surface it and
label where it came from.

### Backend — add a `source` to `GetBaseDomain`

Distinguishing override-vs-env-vs-file needs one bit the handler doesn't have today: *was the
config value set by env or by the YAML file?* Capture it once at load (default is `""`, so
"non-empty after merge but no env" unambiguously means file):

1. **`config/config.go` — new computed field** `BaseDomainSource string` (`yaml:"-"`, not an env or
   file key — derived). In `Load()`, right after the env block applies `VAC_BASE_DOMAIN`
   (`:305-307`):
   - if `os.Getenv("VAC_BASE_DOMAIN") != ""` → `cfg.BaseDomainSource = "env"`
   - else if `cfg.BaseDomain != ""` → `cfg.BaseDomainSource = "file"` (only YAML could have set a
     non-empty value, since the `Default()` is `""`)
   - else leave `""`.
2. **`handler/instance.go` — `GetBaseDomain`** returns a third field `source`:
   - `settings.BaseDomain != ""` → `"override"`
   - else `cfg.BaseDomain != ""` → `cfg.BaseDomainSource` (`"env"` or `"file"`)
   - else `"unset"`.

   Keep the response a small JSON object (it's already `map[string]string`); add `"source"`.
   `PutBaseDomain` (`:168-172`) returns the same shape — after a save the source is always
   `"override"` when `host != ""`, else recompute via the same rule (the override was just
   cleared, so fall back to env/file/unset). Factor the 3-way decision into a tiny unexported
   helper (`baseDomainSource(override string, cfg config.Config) string`) so GET and PUT agree.

### Frontend — surface effective + source, don't silently promote it to an override

`lib/api/instance.ts`:
- Extend `BaseDomainInfo` with `source: 'override' | 'env' | 'file' | 'unset'`.

`domains-section.tsx` `BaseDomainCard`:
- Add an explicit **"Currently effective"** line above/below the input, e.g.
  *"Currently effective: `apps.example.com` — from the `VAC_BASE_DOMAIN` environment variable"* /
  *"…from the config file"* / *"…override set here"* / *"No base domain configured — apps get no
  automatic subdomain."* Map `source` → human label.
- **Do not pre-fill the input with the env/file value.** Keep the input bound to the *override*
  (`current = value ?? data?.base_domain ?? ''`). Pre-filling the effective value would make the
  next Save persist a DB override that *shadows* the env/YAML source — converting "inheriting from
  env" into "pinned override" without the operator intending it. Instead:
  - set the input **placeholder** to the effective value (so the field looks populated/contextual
    while still empty), and
  - when `source !== 'override'`, add a one-line hint: *"Leave blank to keep inheriting from
    `{source}`; type a value to override it here."*
- Gate `WildcardSuggestion` on **`data?.effective`**, not `data?.base_domain`, so the wildcard tip
  shows whenever a base domain is in effect regardless of source.
- The existing change/confirm flow (`changed`, the affected-apps `AlertDialog` at `:110`) already
  handles "this moves N apps' auto URLs" and is correct — leave it. (This is the [domains.md](domains.md)
  note #2 toast; it's already implemented as the confirm dialog + "routes are reconciling" toast at
  `:60`.)

### Tests

- `handler` test for `GetBaseDomain`: (a) override set → `source:"override"`, effective == override;
  (b) `VAC_BASE_DOMAIN` set, no override → `source:"env"`, effective == env value; (c) YAML-only →
  `source:"file"`; (d) nothing → `source:"unset"`, effective `""`. Reuse the existing
  instance-handler test harness.
- `config` test: `Load()` with `VAC_BASE_DOMAIN` set → `BaseDomainSource == "env"`; with only a
  YAML `base_domain` → `"file"`; default → `""`. (Extends the existing
  `TestLoad_*BaseDomain*` cases in `config_test.go`.)
- UI: a component/RTL test (or at least a manual check) that an env-sourced base domain renders the
  "Currently effective … from environment" line and an empty-but-placeholdered input.

### Acceptance

- With `VAC_BASE_DOMAIN=apps.example.com` and no override, the card shows
  *"Currently effective: apps.example.com — from the VAC_BASE_DOMAIN environment variable"*, the
  input is empty with `apps.example.com` as placeholder, and the wildcard tip appears.
- Typing a value and saving sets a DB override (source flips to "override"); clearing it falls back
  to env/file/unset and the label updates — all without a vac-api restart.

---

## P4.2 — Per-app Domains section in app Settings · Effort M

### The gap

Per-app domain CRUD exists at the API and hook layer but is surfaced **only** in global
Settings → Domains. The app's own Settings tab (`app-detail/settings-tab.tsx`) has General /
Source / Build / Runtime / Portability / Danger zone but **no Domains section**, and the app-detail
**Overview** tab shows domains **read-only** (`overview-tab.tsx:73-97`). So an operator on an app's
page can see its domains but can't add/remove one without leaving for global Settings.

Everything needed is already built:
- **Hooks** (`lib/api/domains.ts`): `useDomains(appId)` (custom + derived auto hosts, each with
  live status), `useCreateDomain(appId)` (`{service, hostname}` → `POST
  /apps/{id}/services/{service}/domains`), `useDeleteDomain(appId)`.
- **Services** for the service picker: `useServices(appId)` (already used by the global
  `AddDomainCard`).
- **Reusable presentational components**: `DomainStatusBadge`, `DomainConfigPanel` (the Vercel-style
  "create this A record / Valid configuration / Refresh" card — `settings/domain-config-panel.tsx`),
  and the per-row managed-vs-custom treatment from `domains-section.tsx:288` (`DomainRowItem`).

### Design — extract a shared `AppDomainsSection`, mount it in app Settings

Rather than duplicate the global card, factor a small **`features/settings/app-domains-section.tsx`**
(or `features/app-detail/domains-section.tsx`) that takes `{ appId }` and renders:

1. **List** (`useDomains(appId)`): one row per domain. Reuse the existing row anatomy — hostname +
   `DomainStatusBadge` + a "Configure" toggle that expands `DomainConfigPanel` (the DNS-record
   guidance with the real VPS IP + Refresh). **Managed/auto** rows (`domain.managed === true`) get
   the read-only "Auto" badge and **no delete** (they have no backing row); **custom** rows get a
   Delete button → `useDeleteDomain(appId)` with the existing `confirm(...)` guard.
2. **Add form**: hostname `Input` + a **service `<select>`** populated from `useServices(appId)`
   (the per-app endpoint *requires* a service — unlike the hub's optional/unassigned add). Submit →
   `useCreateDomain(appId)` with `{ service, hostname }`. Disable until hostname looks like a domain
   (`includes('.')`) and a service is picked; toast on success/error; clear the field. Surface the
   `409 hostname already in use` / `404 service not found` messages the handler returns.
3. **Empty state**: "No custom domains. This app is reachable at its automatic subdomain" — and, if
   the app has auto hosts in the list, they already render as managed rows, so this only shows when
   there are none at all.

Mount it in `settings-tab.tsx`:
- Add a `<section><SectionHeader>Domains</SectionHeader> <AppDomainsSection appId={appId} /></section>`
  — place it **after Source/Build, before Runtime** (custom domains are a routing concern adjacent
  to source/build). Render it for **both git apps and add-on apps** (`isAddon`): add-ons are routed
  too and benefit from a custom domain. (Add-ons just have no git Source/Build controls.)
- This is **sync-point #3** with **P1.2** (which adds an "Installed from {template}" panel to the
  same screen). They're additive sibling `<section>`s — agree the vertical order in one of the two
  PRs and keep each section self-contained so the merge is trivial. Suggested order:
  General → Source (P1.2's addon panel *or* the git Source/Build) → **Domains (P4.2)** → Runtime →
  Portability → Danger zone.

> **Reuse over rebuild.** The global `AddDomainCard`/`DomainList` in `domains-section.tsx` target the
> **hub** API (`useAddDomain`/`useAllDomains`, optional unassigned domains). The per-app section
> targets the **per-app** API (`useDomains`/`useCreateDomain`, service required). Don't try to share
> the *card* — share the *row* (`DomainStatusBadge` + `DomainConfigPanel`). If `DomainRowItem` is
> close enough, lift it to a shared component parameterized by the delete handler; otherwise a thin
> per-app row reusing the two presentational pieces is fine and lower-risk.

### Tests

- Hook/RTL: rendering `AppDomainsSection` for an app with one custom + one auto host shows the auto
  row as read-only (no Delete) and the custom row with Delete; adding a domain calls
  `useCreateDomain` with the picked service + hostname; deleting calls `useDeleteDomain`.
- No backend changes → no new Go tests; the existing `domains.go` handler tests already cover
  `ListAppDomains`/`AddCustomDomain`/`DeleteAppDomain`.

### Acceptance

- Each app's Settings tab has a Domains section that lists the app's custom domains and derived auto
  hosts (with live status + the DNS-record guidance panel), lets the operator add a custom domain to
  one of the app's services, and delete custom ones — without going to global Settings. Add-on apps
  get the section too.

---

## Cross-track sync points P4 touches

- **App Settings UI** ([#3](00-parallel-tracks.md)) — P4.2 adds a **Domains** `<section>`; P1.2 adds
  an **"Installed from {template}"** panel. Same screen, additive sections. Agree the section order
  (see P4.2) in whichever PR lands first; keep each section a self-contained component so there's no
  shared-state churn.
- **Migrations** ([#5](00-parallel-tracks.md)) — P4 adds **none**. `BaseDomainSource` is a computed
  config field, not a column.
- No collision with the App DTO ([#1](00-parallel-tracks.md)) / `services.go` ([#2](00-parallel-tracks.md))
  — P4 touches neither.

## Suggested PR breakdown

1. `fix(domains): show effective base domain and its source in the card` — P4.1 (backend `source`
   field + config `BaseDomainSource` + card surfacing). Closes the confirmed display bug.
2. `feat(domains): manage a custom domain from the app's own Settings` — P4.2 (extracted
   `AppDomainsSection`, mounted in the app Settings tab).

PR 1 is the confirmed bug (part of the four-bug critical path); PR 2 is surfacing of capability that
already exists end-to-end and can land any time after.
</content>
</invoke>
