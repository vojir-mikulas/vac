# 06 — Resource guardrails (small-VPS reliability)

**Tier:** Reliability moat · **Effort:** M · **Status:** stub

## Goal

Make the box hard to accidentally OOM. Per-app RAM limits, a visible box-level budget, and
graceful handling of a runaway container.

## Why it matters (strategy)

On a 2 GB box, OOM is the #1 way VAC looks unreliable. Owning that ("VAC stopped a runaway
container before it took down your box") is pure "works amazingly on a cheap VPS."

## Rough shape

- Per-app RAM limit already specced in `mvp.md` (App → Settings → Runtime) — wire it to the
  compose `deploy.resources.limits.memory` / container limit.
- **Box budget UI**: "you've allocated 1.8 of 2 GB" on the dashboard (the mvp's "container
  budget" panel). Warn before over-commit.
- Detect + surface OOM-killed containers distinctly from crash-loops (different exit signal);
  notify.
- Sensible defaults so an unconfigured app can't grab the whole box.

## Open questions

- Where the "box total RAM" figure comes from (host stats already expose it via gopsutil).
- Soft warn vs. hard block on over-allocation.

## Acceptance (sketch)

- Setting a RAM limit enforces it on the container; the dashboard shows allocated-vs-total;
  an OOM event is labelled as such and notified.
