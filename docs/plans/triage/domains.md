# Domains — base domain & per-app assignment

**Status:** triage · **Effort:** S–M (base-domain UI exists; fix display + per-app surfacing)

## Reality check (important — some of this is already built)

- **Set base domain from the UI already exists:** Settings → Domains → base-domain card
  (`ui/src/features/settings/domains-section.tsx:47` `BaseDomainCard`, editable at :89), backed by
  `instanceApi.setBaseDomain` (`ui/src/lib/api/instance.ts:34`) → `PutBaseDomain`
  (`server/handler/instance.go:122`).
- **Changing it does NOT require a vac restart.** `PutBaseDomain` persists to DB, calls
  `pm.SetBaseDomain` on the live proxy, and kicks a background reconcile (30s) that regenerates
  every app's derived auto-routes and prunes orphans (`proxy/manager.go:83`, `:418`). Caddy is
  updated dynamically; no `vac-api` restart needed.
- **Per-app custom domains already have an API:** `ListAppDomains`/`AddCustomDomain`/`DeleteAppDomain`
  (`server/handler/domains.go:87,132,175`).

So the user's belief that base domain "requires a restart of vac" is **stale** — but the report
that it "isn't showing the base domain that is set and probably can't override it" points at a
real **display/precedence bug** worth verifying.

## Notes → actions

1. **BUG? — base-domain card doesn't show the configured value / override feels broken.**
   Base domain resolves with precedence: DB override → `VAC_BASE_DOMAIN` env → YAML → empty
   (`config/config.go:54,295,146`). → Verify the card shows the **effective** value and its
   source ("from env `VAC_BASE_DOMAIN`" vs "override"). If a value is set via env/YAML but the
   card renders empty, that's the bug: the GET should return the effective value, and the input
   should pre-fill it. Add a "currently effective: X (source)" line. **S**

2. **"Restart container if needed, with a notification that it'll restart the instance."**
   Base-domain change reconciles live (no restart). But auto-subdomains for *apps* change, so
   surface a clear toast: "Updated base domain — N apps' URLs changed and routes were
   reconciled" (the handler already returns immediately; reconcile is best-effort). If any case
   genuinely needs a redeploy, gate it behind an explicit confirm. **S**

3. **Per-app domains in the app's own Settings (not only global).**
   The API exists; surfacing is global-only today. → Add a Domains section to each app's Settings
   that lists/edits that app's domains (reuse `ListAppDomains`/`AddCustomDomain`), so management
   isn't only in global Settings → Domains. **M**

## Acceptance sketch

- Base-domain card pre-fills with the effective value and labels its source; editing it shows a
  "reconciled, no restart" toast naming affected apps.
- Each app's Settings has its own Domains section for assigning/removing custom domains.
</content>
