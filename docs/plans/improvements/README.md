# Improvements batch — 2026-05-31

Captured from an operator review of the running dashboard. Each plan below is
self-contained: goal, current-state references (verified against source at this
date), concrete changes, and acceptance criteria.

Design reference for the intended look/flow lives in `design/project/` (static
React mock). These plans translate that mock into the real stack
(`api/` Go + `ui/` React 19 / TanStack / Tailwind 4).

## Plans

| # | File | Scope | Size |
|---|------|-------|------|
| 01 | [01-ui-polish.md](01-ui-polish.md) | Visual/interaction fixes: shadows, cursor, badge, sidebar float + host IP, table header, system theme, copy-key button | S, mostly UI |
| 02 | [02-deploy-fixes.md](02-deploy-fixes.md) | Compose `.yml`/`.yaml` detection + respect configured path; stuck-in-`queued` state; log scroll + error-spam; live deploy preview | M, backend + UI |
| 03 | [03-deploy-adapters.md](03-deploy-adapters.md) | New-app walkthrough + deploy adapters (compose / Dockerfile / framework / static) with auto-detection order | L, backend + UI |
| 04 | [04-env-overhaul.md](04-env-overhaul.md) | Vercel-style env editor: plaintext vs sensitive keys, inline reveal, opt-in secret auto-detect on `.env` import | M, backend + UI |
| 05 | [05-settings-tabs.md](05-settings-tabs.md) | Tabbed settings shell; new Instance + Danger-zone tabs; fold existing notifications / API tokens / sessions in | M, backend + UI |
| 06 | [06-domains-dns.md](06-domains-dns.md) | Instance domains management + per-app DNS-setup guidance ("is this pointed at the VPS yet?") with copy-paste records | M, backend + UI |
| 07 | [07-mock-preview.md](07-mock-preview.md) | Run the whole UI with no backend (MSW + in-memory store + WS simulators) behind a `VITE_MOCK` flag; deploy as a static preview with per-PR URLs | M, UI-only |
| 08 | [08-accessibility.md](08-accessibility.md) | Keyboard + screen-reader pass (skip link, `aria-current`, live logs, labelled forms/meters), `prefers-reduced-motion`, jsx-a11y lint + axe smoke test; plus reusable a11y authoring guidelines for `docs/kb/conventions.md` | M, UI-only |

## Suggested order

1. **01-ui-polish** — cheap, high visible payoff, no schema changes.
2. **02-deploy-fixes** — correctness; unblocks trusting deploys.
3. **04-env-overhaul** and **05-settings-tabs** — independent, can run in parallel.
4. **06-domains-dns** — depends on 05's tab shell.
5. **03-deploy-adapters** — largest; new-app walkthrough depends on adapter backend.

**07-mock-preview** is UI-only and independent of the above — it can run at any
point (best once the main flows it demos are stable). Front-loads no backend work.

**08-accessibility** is UI-only and largely independent. Land its guardrails
(jsx-a11y lint + axe smoke, item 8.1) early so the other plans' new UI is checked
as it's written; the remaining items can follow at any point.

## Explicitly deferred (operator confirmed "later")

- **Charts** (per-app CPU/RAM history, host history) — not now.
- **Onboarding walkthrough** (guided first-deploy, instance bootstrap) — the
  existing admin-account setup at `ui/src/routes/setup.tsx` stays as-is for now.
- **Auto-update / update channels** — Instance tab renders the controls as
  disabled placeholders (see 05); no self-update mechanism is built.
- Settings sections from the mock that have **no backend and aren't requested**:
  Git providers, Backups, Team. Not in scope; omit from the tab list.
