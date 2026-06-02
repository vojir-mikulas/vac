# Security tab & request metrics — why they read as "failing"

**Status:** triage · **Effort:** S–M · Relates to [`../upcoming/15-security-dashboard.md`](../upcoming/15-security-dashboard.md)

The notes: *"I don't feel like the request metrics work"* and *"most of security → ufw,
fail2ban, traffic are failing even though I have fail2ban and ufw installed."* Both have a
root cause in the code — neither is a simple bug.

## 1. ufw / fail2ban / traffic show "not detected" even when installed

This is **expected given the deliberate control-plane sandbox**, not a crash:
- `security/host.go` runs host reads **read-only as a sandboxed process**: `runReadOnly` strips
  the environment (only `PATH`), never inherits `VAC_MASTER_KEY`, with a 3s timeout
  (`host.go:12`).
- `Fail2ban()` (`host.go:74`) and `Firewall()` (`host.go:143`) return `Detected:false` if the
  binary is **absent or unreadable** — and `fail2ban-client` talks to a **root-owned socket**,
  `ufw`/`nft` need root too. The sandboxed non-root control plane can't read them, so it
  silently degrades to "not detected" even though they're installed.

**Fix direction:**
- Make the UX honest: distinguish "not installed" from "installed but VAC can't read it
  (needs a privileged helper)". Don't show a generic failure.
- Provide an opt-in **read-only privileged helper** (a tiny setuid/socket-group shim, or a
  documented `sudoers` line for `fail2ban-client status` / `ufw status`) so these panels can
  actually populate. This is exactly the "open question" flagged in `upcoming/15`
  ("whether fail2ban/firewall reads need a small privileged helper").

## 2. Request metrics feel like they don't work

- Per-IP traffic + anomalies come from the in-memory `security/monitor.go` fed by Caddy access
  logs; `SecurityTrafficHandler` (`server/handler/security.go:43`) returns an **empty snapshot**
  (`TopTalkers: []`, `RecentAnomalies: []`) **with no error** when the monitor is disabled.
- The monitor is gated behind `VAC_SECURITY_MONITOR` — if unset, it's `nil` → always empty.
- Request totals scrape Caddy `/metrics` (`reqmetrics/scraper.go:29`); empty if Caddy metrics
  are unreachable/misconfigured or there's simply been no traffic yet.

**Fix direction:**
- If `VAC_SECURITY_MONITOR` is off, the UI should say "request monitoring is disabled — enable
  `VAC_SECURITY_MONITOR`", not render a silent empty panel that looks broken.
- Verify Caddy JSON access logging is actually on and wired to the collector
  (`reqmetrics.Collector.SetObserver`); surface "no Caddy metrics" vs "no traffic" distinctly.

## 3. Security tab — red badge with a count for items needing attention

UX ask: show a red badge (count) on the Security tab when posture checks fail. → Once the
posture checklist from `upcoming/15` lands, derive a failing-count and badge the nav. **S**

## Acceptance sketch

- Security panels distinguish not-installed / not-readable(needs-helper) / disabled / healthy —
  never a bare "failing."
- An opt-in privileged read path lets fail2ban + ufw panels populate.
- Request-metrics panel says when monitoring is disabled vs. genuinely idle.
- Nav badges the count of failing posture checks.
</content>
