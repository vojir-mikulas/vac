# 03 — Cert-expiry notification (finish D7)

**Tier:** Close the loop · **Effort:** S · **Status:** stub

## Goal

Ship the "TLS certificate expiring within 14 days" notification deferred as deviation D7.

## Why it matters (strategy)

Cheapest remaining MVP-completeness item. Closes a gap between the contract (`mvp.md` §
Notifications) and reality. Trust signal: VAC watches your certs.

## Rough shape

- Needs reliable per-host `not_after`. Phase 3 tracks `domains.cert_status` as advisory
  only — the missing piece is a **Caddy PKI read-back** exposing real expiry per host.
- Once expiry data exists, wire a new event into the existing `notify` dispatcher
  (cheap — the dispatcher already handles deploy ok/fail, crash-loop, restarted).
- Background check on a schedule (reuse the retention/nightly goroutine pattern).

## Open questions

- Exact Caddy admin endpoint / PKI read for per-host `not_after`.
- De-dupe: fire once per cert per threshold crossing, not nightly until renewed.

## Acceptance (sketch)

- A cert within 14 days of expiry fires one Discord/Slack notification; auto-renewal
  clears the state without spamming.
