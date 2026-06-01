# 09 — Vercel-like domain management

**Goal:** Make domains a coherent, first-class thing the operator *manages* — add a
domain, see exactly what DNS to set, watch it verify itself, assign it to an app, and
never be left with stale junk. Today domain management is functional but clunky: domains
only exist buried under an app/service, base-domain changes leave orphans, status is a
guess, and you can't even delete from the UI. This plan brings it up to the Vercel bar.

Builds on plan **06** (which shipped the Domains tab, base-domain control, DNS guidance,
`/dns-check`). This is the lifecycle + UX overhaul on top of it.

## What "Vercel-like" means here (and what it deliberately is *not*)

Vercel's domain UX, mapped onto a single-VPS, single-operator box:

**We adopt:**
- Domains are added and managed in one place, then **assigned** to an app/service.
- Each domain shows a clear **DNS configuration check** — the exact record to create
  (apex → `A`, subdomain → `CNAME`/`A`), the real VPS IP, and **Valid / Invalid /
  Pending** status that updates itself (auto-poll), not a one-shot button.
- **Apex + www** handled as a pair, with a **primary domain** and the other redirecting
  to it (e.g. `www.example.com` → `example.com`).
- **Reassign** a domain between apps/services without a destructive delete-add.
- **Wildcard** domains for auto-subdomains, surfaced as a guided step.

**We deliberately skip** (single-box reality — call these out so nobody "adds them back"):
- **Nameserver delegation.** We only do A/CNAME records; VAC is not a DNS provider.
- **TXT ownership verification.** On a single box, "you control the DNS *and* you added
  it in VAC" is sufficient proof, and cert issuance is already gated by `CaddyAsk`
  (on-demand TLS). No TXT challenge needed.
- **Multi-team / domain transfer / marketplace.** One operator.

Document the adopted/skipped split in `docs/deviations.md`.

---

## Foundation — fix the lifecycle (must land first)

These are prerequisites; the Vercel-like UX sits on top of a model that can't produce
orphans and tells the truth about status.

### F1 — Derive auto-subdomains, don't store them

**Auto-subdomains become a pure function of `(app slug, app's HTTP services, current
base domain)`, computed at reconcile time — not rows in `domains`.**

- The `domains` table holds **custom domains only**.
- At reconcile/sync, the proxy manager enumerates each app's HTTP services and, if a base
  domain is set, generates the auto host(s) (`proxy.AutoSubdomain`) and emits Caddy
  routes for them, with route IDs derived from app/service rather than a domain UUID
  (today `routeID` is `vac-route-{domain.ID}`, `proxy/network.go:16` — add a parallel
  `vac-auto-{appID}-{service}` scheme).
- `CaddyAsk` (`handler/caddy_ask.go`) accepts a hostname if it's a known custom domain
  **or** matches a currently-derived auto host.

**Why:** changing the base domain becomes a no-op beyond "reconcile" — the auto route set
regenerates from the new base and old routes are pruned by the existing `pruneOrphans()`
(`proxy/manager.go:249`). **Orphans become structurally impossible**, and existing apps
pick up the new base immediately, no redeploy. Removes the lazy `AssignAutoDomains`-at-
deploy coupling (`proxy/manager.go:380`, `deploy/pipeline.go:300`).

**Trade-off:** per-auto cert status loses its row — track it in-memory/recompute (auto
hosts sit under the operator's own wildcard, lower stakes). One-time migration: drop
existing `type='auto'` rows and prune their routes on first boot; `type='custom'` rows
untouched. Document in `docs/deviations.md`.

> **Open decision:** if a join on `domains.hostname` turns out to need auto rows present,
> fallback is to *keep* the rows but transactionally reconcile on base-domain change
> (delete all `type='auto'`, re-run `AssignAutoDomains` per app, `Reconcile()`). Fixes
> orphans too, keeps the lazy-row complexity. Prefer "derive." (Verify before committing.)

### F2 — Base-domain change flow

- On `PUT /api/instance/base-domain` (`handler/instance.go:100`): after persist +
  `SetBaseDomain`, trigger `Reconcile()` so routes reflect the new base immediately.
- **UI confirm before save:** "N apps will move from `*.old` to `*.new`. Old URLs stop
  working. Continue?" — preview computed from the app list.
- **Clearing the base domain** warns and lists the apps whose auto URL will stop resolving.
- **Suggest the wildcard record.** Auto-subdomains only resolve if `*.{base}` points here,
  so the base-domain control must guide it, not bury it: show `*.{base}` → `A` → real VPS
  IP (or `CNAME` → base host), copy-pasteable (IP from plan **01.4**). Make it actionable:
  "Want every app to get an automatic subdomain? Add a wildcard `*.{base}` record pointing
  here," with a Check-DNS affordance that probes a throwaway label (e.g.
  `_vac-wildcard-check.{base}`) since a bare `*` can't be resolved.

### F3 — One honest status (the DNS-detection engine)

This is the core of the Vercel feel. Replace the overloaded advisory `cert_status`
(`store/domains.go`, set to `error` only on route-push failure, never reliably `active`)
with a single **derived domain status** the UI shows without the operator guessing:

| Status | Meaning |
|--------|---------|
| `awaiting_dns` | hostname does not resolve to this VPS yet |
| `misconfigured` | resolves, but to a different IP / wrong record type |
| `issuing` | points here, cert not yet observed active |
| `active` | DNS valid **and** cert observed valid + serving |
| `error` | route push failed or cert issuance failed (show the reason) |

The detection engine knows what *should* exist and checks it:
- **Apex domain** (`example.com`) → must be an `A` record to the VPS IP (CNAME-at-apex is
  invalid) — detect and say so explicitly.
- **Subdomain** (`app.example.com`) → `CNAME` to the base host **or** `A` to the VPS IP.
- Run it as a **lightweight background reconciler** over known custom domains + derived
  auto hosts (bounded concurrency, short cache), reusing the resolver from `/dns-check`
  (`handler/instance.go:184`) — so status updates itself instead of only on button click.
- **Cert signal:** reuse the cert-expiry probe (dials proxy SNI to read leaf cert
  metadata, migration **00033**) to flip `issuing → active` and populate `cert_not_after`.
- Surface the **actual error text** for `error`/`misconfigured` (today the red badge has
  no detail) so the operator knows whether it's DNS or issuance.

---

## Phase 1 — The Domains screen (add, verify, assign)

The Settings → Domains tab becomes the Vercel-style hub.

- **Add a domain** anywhere (not gated behind picking an app first): enter hostname,
  validated via `proxy.NormalizeHostname`. It lands in a list immediately with status
  `awaiting_dns` and an inline **configuration panel**.
- **Configuration panel per domain** (the Vercel "Valid/Invalid Configuration" card):
  - The exact record to create — Type / Name / Value — with the **real VPS IP**
    (host-IP source, plan **01.4**), copy-to-clipboard, apex-vs-subdomain aware (F3).
  - Live status badge from F3 that auto-refreshes (poll while not `active`), plus a manual
    "Refresh" for impatience.
  - On `active`: green "Valid Configuration ✓" and the record table collapses.
  - On `misconfigured`: "Resolves to X — expected this server," with the right record shown.
- **Assign to app + service:** a domain row carries its `app_id` + `service_name`
  (existing composite FK). The add flow lets you pick the target; the row shows
  `{app} · {service}`. A domain may be added *unassigned* and assigned later (decide:
  nullable assignment vs. assign-on-add; nullable is more Vercel-like).
- **List shows everything** across apps: hostname, app/service, status, type
  (custom vs. managed/auto), actions. Auto hosts are shown **read-only/managed** — you
  change them by changing the slug or base domain, never by hand.

## Phase 2 — Editing & reassignment

No update path exists today (`store/domains.go` is create/list/get/delete only) — add one.

- **`UpdateDomain`** store method + **`PATCH /api/apps/{id}/domains/{domainId}`** handler
  to change the **service binding** (re-point `api.example.com` from `web` → `api`) and/or
  rename the hostname, then re-sync — an **in-place route swap**, not delete-add, so there's
  no route gap or needless cert re-issue.
- **Move between apps:** reassigning `app_id` (guard the composite FK; the target
  app/service must exist). Surface as "Move domain to another app."
- **Wire up delete** (the existing `useDeleteDomain()` hook is imported nowhere): delete
  from both the Domains tab and the app-detail panel, with confirm. Custom only — auto
  hosts aren't deletable.
- **App-detail parity:** `app-detail/overview-tab.tsx` domains panel is read-only today;
  let custom domains be added/removed/reassigned there too (or link clearly to the hub).

## Phase 3 — Apex + www and redirects (the polish)

Vercel's "add both, pick a primary, redirect the other." Requires new plumbing — the
current route is **Host-matcher + reverse_proxy only** and the `caddy.Handler` struct
(`caddy/config.go`) has **no fields for a redirect** (only `Handler`, `Upstreams`,
`HealthChecks`).

- **Model:** add a nullable `redirect_to` (hostname) to `domains`. If set, the domain
  emits a **redirect route** instead of a proxy route. A "primary" domain is simply the
  one others redirect to.
- **Caddy types:** extend `caddy.Handler` with `static_response` fields — `StatusCode int`,
  `Headers map[string][]string` (for `Location`) — all `omitempty`. Add a `redirectRoute`
  builder alongside `routeFor` (`proxy/manager.go:154`) that emits a 308 to the primary.
- **UX:** when adding `example.com`, offer to also add `www.example.com` (and vice versa),
  pick the primary, auto-create the redirect for the other. Show both in the list with a
  "Primary" badge and "Redirects to …" subtext.

Phase 3 is optional / can land later — Phases F + 1 + 2 already deliver the bulk of the
"add domains in settings, check DNS properly, manage them" experience.

---

## Acceptance criteria

- Operator can add a domain in Settings, see the exact DNS record (real VPS IP, apex-vs-
  subdomain correct), and watch it flip to **Valid** on its own once DNS points here — no
  manual polling required, and `misconfigured`/`error` explain why.
- Changing the base domain updates every app's auto URL immediately with **zero** stale
  rows/routes, after a confirm that names the affected apps; setting a base domain surfaces
  a verifiable wildcard suggestion.
- Custom domains can be **added, reassigned (service or app), renamed, and deleted** from
  the UI; auto hosts are shown managed/read-only.
- (Phase 3) Apex + www can both be added with one chosen primary and the other redirecting.

## Verification

- Go: derived auto-host generation matches old `AutoSubdomain`; base-domain change leaves
  no orphan routes (mock Caddy + store); `CaddyAsk` accepts derived auto hosts; DNS-detection
  classifies apex-A vs subdomain-CNAME vs wrong-IP correctly (mock resolver); `UpdateDomain`
  reassign swaps the route in place; (P3) redirect route emits a 308 to primary.
- Integration: set base A → deploy app → set base B → reachable only on B, A's route gone,
  no leftover rows.
- `make test`, `make typecheck`, `make lint`.
- Manual: add custom domain not pointed → `awaiting_dns` + record card; point it → auto-flips
  to Valid + cert active; reassign service → no downtime; delete → gone from list and Caddy.

## Cross-refs / non-goals

- Builds on plan **06**; host-IP source plan **01.4**; settings tab shell plan **05**.
- Orphan pruning `proxy/manager.go:249`; route build `proxy/manager.go:154`; on-demand TLS
  gate `handler/caddy_ask.go`; cert probe migration **00033**; architecture invariants
  (Caddy owns routing/health, vac-api off `vac-edge`) `CLAUDE.md`.
- **Non-goals:** nameserver delegation, TXT ownership verification, multi-team/transfer.
