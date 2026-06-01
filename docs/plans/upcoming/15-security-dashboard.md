# 15 — Security dashboard (read-only posture & traffic signals)

**Tier:** Trust moat · **Effort:** M · **Status:** stub

## Goal

A "Security" dashboard tab that surfaces — read-only — the box's security posture and
suspicious traffic. No write access to host firewall/fail2ban in this cut: VAC *shows and
alerts*, the operator acts. This keeps the control plane sandboxed (it stays off `vac-edge`
and out of privileged host mutation) while still being useful.

## Why it matters (strategy)

VAC already sits at the two best vantage points on the box — **Caddy** (sees every request)
and the **build pipeline** (builds every image). Surfacing that as "your box at a glance,
security-wise" is high trust-per-effort and on-brand for "works amazingly on a cheap VPS,"
without dragging in a SAST product or real DDoS mitigation (a single VPS can't absorb
volumetric attacks — that's Cloudflare's job; we do *detection + alerting*).

## Rough shape (four read-only panels)

1. **Posture checklist** *(easiest, most on-brand)* — a static rules pass over VAC's own
   config: exposed/published host ports, apps missing TLS, weak/default settings, master key
   present, etc. Pure rules engine, no external deps.
2. **Traffic anomaly / DDoS signals** *(highest value-per-effort)* — turn on Caddy JSON
   access logs and compute rolling per-IP / per-app counters in-process: RPS spikes, 4xx/5xx
   surges, top talkers, odd UAs/paths. Feed thresholds → alerts (reuse Discord/Slack
   notifications + the Prometheus metrics already wired). Streaming counters, stays in the
   RAM budget.
3. **fail2ban status (read-only)** — parse `fail2ban-client status <jail>` (or its socket):
   banned IPs, jail counts. Source jail off Caddy access logs. Display only — no add/remove
   bans yet.
4. **Firewall view (read-only)** — show host ufw/nftables rules and open ports. Display only.

## Out (deferred to a later cut)

- **Write** access: ban/unban from UI, editing firewall rules. Jumps the difficulty — needs
  privileged host access + "don't brick the operator's box" guardrails, which rubs against
  the deliberate control-plane sandboxing. Separate decision.
- Container image CVE scanning (Trivy/Grype post-build) — useful but heavier (scanner dep,
  RAM/CPU spikes, result storage). Could be its own stub.
- App-code SAST — out of scope entirely.

## Open questions

- Where Caddy access logs land and how VAC tails them (path vs. socket vs. log module).
- Whether fail2ban/firewall reads need a small privileged helper or are readable as-is.
- Counter window sizing + thresholds that don't false-positive on a busy small app.

## Acceptance (sketch)

- A Security tab shows the posture checklist with pass/fail rules, a live traffic panel with
  per-IP rates and anomaly flags, and (if present on the host) read-only fail2ban + firewall
  state. A traffic anomaly fires a notification. Nothing in this cut mutates host state.
