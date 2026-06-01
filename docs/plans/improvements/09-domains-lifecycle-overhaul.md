# 09 — Domains lifecycle overhaul

**Goal:** Fix the rough edges that plan 06 (now shipped) left behind. Make domain
management feel coherent: changing the base domain should never leave stale/half-wrong
domains around, every domain should have one honest status, and the operator should be
able to see, understand, and remove domains from the UI.

Plan 06 delivered the *surface* (Domains tab, base-domain control, DNS guidance,
`/dns-check`). This plan fixes the *lifecycle* underneath it.

## Current state (the pain)

- **Auto-subdomains are persisted rows, created lazily at deploy.**
  `proxy.AutoSubdomain` computes `{slug}.{base}` (or `{service}.{slug}.{base}`),
  but the result is written into the `domains` table by `AssignAutoDomains`
  (`proxy/manager.go:380`) only during a deploy. Nothing recomputes them otherwise.
- **Changing the base domain does no cleanup.** `PUT /api/instance/base-domain`
  (`handler/instance.go:100`) persists the value and calls `pm.SetBaseDomain`, then
  returns. Existing `app.old-base` rows stay in the DB *and* in Caddy. New
  `app.new-base` rows only appear when each app is next deployed → both coexist,
  both route to the same upstream, and the table grows on every base-domain change.
  This is the "hanging old domains" the operator sees.
- **`cert_status` is advisory-only.** It defaults to `pending` (`store/domains.go:15`)
  and is set to `error` on a Caddy route-push failure (`manager.go:243`). Nothing
  reliably flips it to `active`. "pending" is overloaded: DNS-not-pointed, issuing,
  or just-never-updated all look identical.
- **`/dns-check` is one-shot and stateless** (`handler/instance.go:184`) — it reports
  to the client and persists nothing.
- **No delete UI.** `useDeleteDomain()` exists (`ui/src/lib/api/domains.ts`) but is
  imported nowhere. Domains can't be removed from the dashboard.
- **Auto vs custom is invisible** in the UI, so stale auto rows are indistinguishable
  from intentional custom domains.

## The core decision: derive auto-subdomains, don't store them

**Auto-subdomains become a pure function of `(app slug, app's HTTP services, current
base domain)`, computed at route-reconcile time — not rows in `domains`.**

- The `domains` table holds **custom domains only**.
- At reconcile / sync, the proxy manager enumerates each app's HTTP services and, if a
  base domain is set, generates the auto host(s) on the fly and emits Caddy routes for
  them (route IDs derived from app/service rather than a domain UUID).
- `CaddyAsk` (`handler/caddy_ask.go`) accepts a hostname if it is a known custom domain
  **or** matches a currently-derived auto host.

**Why this is the right call:**
- Changing the base domain becomes a no-op beyond "reconcile" — the full auto route set
  regenerates from the new base and the old routes are pruned by the existing
  `pruneOrphans()` (`manager.go:249`). **Orphans become structurally impossible.** No
  migration, no cleanup job, no "stale" badge, no accumulation.
- Existing apps pick up the new base domain immediately on reconcile — no redeploy
  needed (closes the "existing apps keep old domains until redeploy" gap).
- Removes the lazy `AssignAutoDomains`-at-deploy coupling.

**Trade-off / what we give up:**
- Per-auto-domain `cert_status` no longer has a row to live on. Auto domains sit under
  the operator's own wildcard, which they control, so their cert state is lower-stakes —
  track it in-memory / recompute rather than persist (see Part B). Custom-domain cert
  status keeps its column.
- One-time migration: drop existing `type='auto'` rows (they're now derived) and prune
  their Caddy routes on first boot. `type='custom'` rows are untouched. The `type`
  column / CHECK can stay for back-compat or be dropped — decide during impl.
- Document in `docs/deviations.md` (auto-subdomains are derived, not persisted).

> **Open decision:** if persisting auto rows turns out to be load-bearing for something
> not yet found (e.g. a metrics join on `domains.hostname`), the fallback is to *keep*
> the rows but make base-domain change transactionally reconcile them: delete all
> `type='auto'` rows, re-run `AssignAutoDomains` for every app, then `Reconcile()`. This
> fixes the orphan problem too but keeps the lazy-row complexity. Prefer "derive" unless
> a join forces "reconcile rows."

## Part A — Base-domain change flow

- On `PUT /api/instance/base-domain`: after persisting + `SetBaseDomain`, trigger a full
  `Reconcile()` synchronously (or enqueue and report status) so routes reflect the new
  base immediately.
- **UI confirmation before save.** The base-domain control currently saves silently.
  Add a confirm step that shows the consequence concretely: "N apps will move from
  `*.old` to `*.new`. Old URLs will stop working. Continue?" Compute the preview from
  the app list + current/next base domain.
- **Clearing the base domain** (disabling auto-subdomains) should warn that every app's
  auto URL will stop resolving, and list which apps that affects.
- **Suggest the wildcard DNS record.** Auto-subdomains only resolve if the operator
  points `*.{base}` at this VPS, so the base-domain control must guide that explicitly,
  not bury it. When a base domain is set (or being set), show the exact record to create
  alongside the apex/host record:
  - `*.{base}` → `A` → **the VPS public IP** (or `CNAME` → the base host), with the real
    IP from the host-IP source (plan **01.4**) so it's copy-pasteable, not a placeholder.
  - Make it an actionable suggestion: "Want every app to get an automatic subdomain? Add
    a wildcard `*.{base}` record pointing here." — with a "Check DNS" affordance (reuse
    `/dns-check`) that confirms the wildcard resolves to this VPS, e.g. by probing a
    throwaway label like `_vac-wildcard-check.{base}`.
  - The base-domain UI already mentions a wildcard in passing
    (`settings/domains-section.tsx`); this elevates it to a first-class, verifiable step
    so the operator isn't surprised when `whoami.{base}` doesn't resolve.

## Part B — One honest domain status

Replace the overloaded `cert_status` surface with a single derived **domain status** the
UI can show without guessing:

- `awaiting_dns` — hostname does not resolve to this VPS yet.
- `issuing` — DNS points here, cert not yet observed active.
- `active` — cert observed valid and serving.
- `error` — last route push failed, or cert issuance failed.

Source it from two signals VAC already has access to:
- **DNS:** reuse the `/dns-check` resolver logic (`handler/instance.go:184`) but run it
  in a lightweight background reconciler for known domains and cache the result, instead
  of only on button click.
- **Cert:** the cert-expiry checker already dials the proxy SNI to read leaf cert
  metadata (`migration 00033`, `store/domains.go` cert fields). Reuse that probe to mark
  `active` and feed `cert_not_after`.

Keep it cheap: a periodic reconcile pass (custom domains from the table + derived auto
hosts), bounded concurrency, short cache. No per-request probing.

Surface the actual error text for `error` status (today the red "SSL error" badge has no
detail) so the operator knows whether it's DNS or issuance.

## Part C — UI: see, understand, remove

- **Wire up delete.** Use the existing `useDeleteDomain()` in both the Settings →
  Domains list and the app-detail domains panel. Confirm on delete. (Custom domains
  only — auto hosts are managed/derived and shown read-only.)
- **Distinguish auto vs custom.** Tag derived auto hosts as "managed" / read-only;
  custom domains get the delete affordance. Removes the "is this junk or intentional?"
  ambiguity.
- **Status → action.** Map the Part B status to a clear badge + next step:
  `awaiting_dns` → show the DNS record to create (Part C of plan 06 already has the
  guidance component) and a re-check button; `issuing` → "DNS looks good, cert issues on
  first request (~60s)"; `error` → show the reason.
- **App-detail parity.** The overview domains panel (`app-detail/overview-tab.tsx`) is
  read-only today; let custom domains be added/removed there too, or link clearly to the
  settings tab.

## Acceptance criteria

- Changing the base domain updates every app's auto URL immediately, with **zero** stale
  rows or stale Caddy routes left behind, and the operator confirmed the change knowing
  which apps move.
- Clearing the base domain warns and lists affected apps before disabling auto-subdomains.
- Setting a base domain surfaces the exact wildcard record (`*.{base}` → real VPS IP) as
  a suggested, "Check DNS"-verifiable step so auto-subdomains actually resolve.
- Every domain shows exactly one status (`awaiting_dns | issuing | active | error`) that
  reflects reality without the operator clicking "Check DNS", and `error` shows why.
- Custom domains can be deleted from the UI (with confirm); auto hosts are shown as
  managed/read-only and never need manual cleanup.

## Verification

- Go test: base-domain change reconciles routes and leaves no orphan routes (mock Caddy
  + store); derived auto-host generation matches old `AutoSubdomain` output;
  `CaddyAsk` accepts derived auto hosts.
- Integration: set base domain A, deploy app, set base domain B → app reachable only on
  B, A's route gone from Caddy, no leftover rows.
- `make test`, `make typecheck`, `make lint`.
- Manual: set base domain, observe existing app's URL flips without redeploy; add custom
  domain not yet pointed → `awaiting_dns` + guidance; point it → flips to `active`;
  delete it → gone from list and Caddy.

## Cross-refs

- Builds on plan **06** (domains tab, DNS guidance, `/dns-check`, base-domain control).
- Orphan-route pruning: `proxy/manager.go` `pruneOrphans()`. On-demand TLS gate:
  `handler/caddy_ask.go`. Cert-expiry probe: migration **00033**.
- Architecture invariants (Caddy owns health/routing, vac-api off `vac-edge`): `CLAUDE.md`.
