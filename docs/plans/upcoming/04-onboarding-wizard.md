# 04 — Onboarding wizard (finish the guided flow)

**Tier:** Close the loop · **Effort:** M · **Status:** stub

## Goal

Complete the multi-step first-run flow specced in `mvp.md` § Onboarding. Today only the
admin-account setup (`ui/src/routes/setup.tsx`) exists; the guided remainder is deferred.

## Why it matters (strategy)

"Fast onboarding / minimal friction" verbatim. First-run is where trust is won or lost —
a solo dev should get to a live deploy without reading docs.

## Rough shape

Per `mvp.md`, the steps after account creation:
- **Instance setup** — name, timezone; DNS checklist ("point an A record here").
- **SSH key** — show the global deploy key + copy button + links to GitHub/GitLab docs;
  skippable for public-repo-only users.
- **First deploy** — inline mini app-creation: paste repo URL → branch → domain → one
  "Deploy" button into the live build-log view.
- Dismiss permanently once complete/skipped.

## Open questions

- Reconcile with the "operator confirmed: leave setup as-is for now" note in
  `docs/plans/improvements/README.md` — confirm appetite before building.
- Reuse the new-app walkthrough from improvements plan 03 for the "first deploy" step.

## Acceptance (sketch)

- A fresh instance walks a new operator from account → instance → key → first live deploy
  without leaving the wizard.
