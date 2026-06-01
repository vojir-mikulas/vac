# 01 — Push-to-deploy (Git webhook + trigger model)

**Tier:** Close the loop · **Effort:** L · **Status:** stub

## Goal

Push to Git → VAC deploys, with no manual button. The single highest-leverage feature
not yet built. Turns VAC from "a deploy tool" into "my deployment platform" and is the
expectation set by every modern PaaS.

## Why it matters (strategy)

Directly serves "minimal friction / fast iteration / opinionated workflows." It's the
feature that changes how VAC *feels*.

## Rough shape

- **Trigger model, not a bool.** Per-app list of rules: `{ event, filter }` where
  `event ∈ push | tag | manual`, `filter` is a branch/tag glob or regex.
  - e.g. `push:main`, `push:release/*`, `tag:v*`, `tag:semver-only`.
  - Existing `apps.git_branch` becomes "default branch for manual deploys."
- **Inbound webhook endpoint** — generic first (works with any Git host), then
  GitHub/GitLab signature verification. Payload carries ref + ref-type; VAC matches it
  against the app's trigger rules → deploy or ignore.
- Surface the inbound webhook URL + secret in Settings → Source (the mvp already specs
  the toggle + URL there).
- Debounce / coalesce rapid pushes; respect the single-deploy-at-a-time worker queue.

## Open questions

- Schema: new `deploy_triggers` table vs. JSONB column on `apps`.
- Secret storage (encrypt the webhook secret like other secrets).
- How to surface "ignored this push because ref didn't match" in the activity feed.

## Acceptance (sketch)

- A push to a matching branch auto-creates a deployment; a non-matching ref is ignored
  and logged. Tag-only apps deploy on `v1.2.3` but not on a branch push.
