# 06 — Domains management + DNS setup guidance

**Goal:** Two related things:
1. Instance-level **Domains** tab in settings (list domains, their apps, cert
   status, add custom domain).
2. Per-app **DNS guidance** at the healthcheck/domain step — answer "is this
   domain actually pointed at the VPS yet?" with a copy-paste tutorial of the DNS
   records to set.

## Current state

- **Domains backend exists:** `domains` table (`00012_domains.sql`),
  `store/domains.go`, `handler/domains.go` (`AddCustomDomain`, list, etc.).
  Domains attach to a **service** (composite FK on `app_id, service_name`).
  Types: `auto` (VAC-assigned subdomain via `proxy.AutoSubdomain`, needs
  `BaseDomain` configured) or `custom`. Cert status `pending|active|error`
  (advisory).
- **Routing/TLS:** Caddy owns routing by DNS alias and issues certs via
  on-demand TLS (`handler/caddy_ask.go`) once DNS points at the proxy host.
- **Gaps:** No instance-wide domain list UI. No DNS-setup guidance anywhere —
  the operator has no in-app answer to "what record do I create and where does it
  point?" Auto-subdomains need `BaseDomain` but there's no UI to set it.

## Part A — Settings → Domains tab

(Lives in plan 05's tab shell. Design: `view-settings.jsx` `SecDomains`.)
- List all domains across apps: hostname, attached app/service, SSL status
  (`active|pending|error` → Badge), and an actions menu.
- **Add domain** button → form: hostname (validated via existing
  `proxy.NormalizeHostname`), target app+service. Calls `AddCustomDomain`.
- Surface cert status from the existing advisory field; show pending/error
  clearly (error → likely DNS not pointed yet → link to the DNS guide, Part C).
- **Base domain config:** add a control to set the instance `BaseDomain` (used
  for auto-subdomains). Today it's config-only — expose it here. Needs a small
  backend: read/write instance base domain. Decide storage — a singleton
  instance-settings row (new migration) vs config; recommend a DB row so it's
  runtime-editable, and have the proxy manager read it. Document in
  `docs/deviations.md`.

## Part B — New-app & app-detail domain step

- The new-app **Domain** step (plan 03 UI) and the app-detail domain/settings
  area should show the same DNS guidance (Part C) right where a custom domain is
  entered.

## Part C — DNS setup guidance ("is it pointed at the VPS?")

When a custom domain is added or shown with `pending`/`error` cert status,
display a concrete, copy-paste tutorial:

- **What to create:** the exact DNS record(s):
  - Apex domain → `A` record → **the VPS public IP**.
  - Subdomain (`app.example.com`) → `A` to the VPS IP, or `CNAME` to the base
    domain / host. (Match the design hint copy: "Point a CNAME from your domain
    to … — certificates are issued within 60 seconds of the first request.")
  - Show the actual VPS IP (same host-IP source added in plan 01.4) so the record
    value is copy-pasteable, not a placeholder.
- **Live status check:** a "Check DNS" affordance that resolves the hostname and
  reports whether it currently points at this VPS.
  - **Backend:** `GET /api/apps/{id}/services/{name}/domains/{host}/dns-check`
    (or a query endpoint) that resolves the hostname server-side and compares to
    the VPS public IP → `{resolved: [...], pointsHere: bool, ip}`. Place in
    `handler/domains.go`. Keep it a read-only lookup; cache briefly.
  - **UI:** show ✅ "Pointed at this server" / ⚠️ "Not pointing here yet — add the
    record above and recheck", plus the cert status. Re-checkable button.
- **Cert note:** explain HTTPS is automatic via Caddy on-demand TLS once DNS
  resolves to the host, so the typical sequence is: add record → DNS propagates →
  first request issues the cert.

## Acceptance criteria
- Settings → Domains lists domains with status and supports adding a custom domain.
- Base domain for auto-subdomains is settable in the UI and respected by the proxy.
- Adding a custom domain shows the exact record to create (with the real VPS IP)
  and a working "Check DNS" that reports whether it points at the VPS.
- A domain stuck in `pending`/`error` surfaces the DNS guidance instead of a bare
  error.

## Verification
Go test for the DNS-check handler (resolve + compare; mock resolver); handler test
for base-domain read/write; `make typecheck`; manual: add a domain not yet
pointed → see guidance + "not pointing here", point it → recheck flips to ✅ and
cert goes active.

## Cross-refs
- Host IP source: plan **01.4**. Tab shell: plan **05**. New-app domain step: **03**.
