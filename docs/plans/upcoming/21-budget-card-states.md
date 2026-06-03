# 21 — Container budget card: honest visual states + badges

**Tier:** Observability / UX polish · **Effort:** S · **Status:** detailed (ready to build)

## Goal

Fix the **Container budget** card on the apps dashboard so its colours mean what an operator
expects, and turn one passive bit of muted text into a glanceable **badge**. Two small,
tightly-coupled tweaks on the same card (`ui/src/features/apps/apps-dashboard.tsx:199-254`):

1. **"Running apps" meter colour is inverted.** The running-apps row reuses the generic
   `Meter`, which flips to **red above 80% utilisation** (`meter.tsx:23-25`). So "2/2 apps
   running" — the *healthy, fully-up* state — renders **red** as if it were a problem. That's
   backwards. The fix isn't to invert the meter: the row is **occupancy, not utilisation**, so
   it shouldn't be a "filling toward a cap" bar at all. Replace it with a **health-toned count
   badge** (`2/2`): all-up → green, real issues → amber, intentional-stops → neutral, never red.

2. **"N apps without a RAM limit aren't budgeted" should be a badge, not muted text.** Today
   it's `text-2xs text-muted-foreground` (`apps-dashboard.tsx:238-244`) — easy to miss. Make it
   a **blue info badge with an info icon** ("informational, not an error"), kept strictly below
   the red over-commit warning so severity reads red > blue.

## Why it matters (strategy)

Trust through honest signals. Colour is the fastest channel on a dashboard; when "everything
is up" glows red, the operator learns to distrust the colours — the opposite of the
reliability/trust moat. Cheap fixes, outsized effect on how trustworthy the box feels at a
glance.

## Current state (verified against source)

- **Budget card:** `apps-dashboard.tsx:199-254`. The running-apps row is a `BudgetRow`
  (`:203-208`, helper at `:343-369`) wrapping `Meter` with `tone="brand"`.
- **Meter red-at-80%:** `meter.tsx:22-25` — `const over = clamped > 80; fill = over ? 'bg-err'
  : tone === 'brand' ? 'bg-brand' : 'bg-foreground'`. The red flip is **correct** for the three
  genuine utilisation rows below it (Host RAM, Disk, Allocated RAM) — leave `Meter` untouched so
  those keep their over-threshold warning. Only the occupancy row misuses it.
- **Counts:** `counts = countByFilter(list)` (`apps-dashboard.tsx:42`) returns
  `{ all, running, issues, stopped }` (`status-filter.ts:20-30`). `issues` = crashed / degraded
  / failed / interrupted (`ISSUE_STATUSES`, `status-filter.ts:5`). This gives us everything we
  need to tone the badge by health without a new API call.
- **Unbudgeted text + over-commit warning:** `apps-dashboard.tsx:234-244`, driven by
  `budget.over_committed` and `budget.apps_total > budget.apps_with_limit` from
  `GET /api/host/budget` (`api/internal/server/handler/host_budget.go:28-44`). The red
  over-commit `<p className="text-err">` (`:234-237`) already takes precedence in the ternary —
  keep that ordering. **No backend change is needed for this plan.**
- **Brand is blue, not orange.** `--brand: #0057b8` (`index.css:121,179`) — the "orange" code
  comments are stale. So blue is already a first-class accent; an info badge is in-palette.
- **Design tokens:** `ok` / `warn` / `err` each ship a full `{base, -fg, -bg, -border}` family
  (`index.css:124-138` light, `182-196` dark) wired into Tailwind utilities via `@theme inline`
  (`index.css:65-77`). **`brand` has only `--brand` + `--brand-foreground`** — no `-bg`/`-border`,
  so there is no soft-blue badge surface yet. There is **no `info` colour family**.
- **Badge:** `components/ui/badge.tsx` — variants `default / secondary / success / destructive /
  outline / ghost / link`. `success` is the green soft badge (`bg-ok-bg text-ok-foreground`).
  **No blue `info` variant** — confirmed absent (this is the same gap plan 20 calls out at
  `20-database-section.md:209-210`; the token + variant add is **shared** with plan 20).
- **StatusPill tones** (`components/common/status-pill.tsx:13-42`): `running → ok` (green),
  `degraded/building → warn` (amber), `stopped/canceled → muted`, `crashed/error → err`. Our
  badge tone mapping should align with these so a "1/2 running" badge reads the same colour
  language as the status pills in the table.
- **Icons:** Lucide is already imported in this file (`apps-dashboard.tsx:3`); add `Info`.

## Decisions (resolved from the open questions)

- **Occupancy is a badge, not a bar.** A bar implies "filling toward a cap," true for the RAM
  rows, false for "apps up." Drop the `Meter` for this row; render `{running}/{all}` as a toned
  count badge. (Keeps the genuine-utilisation rows as bars — the visual distinction is now
  *meaningful*.)
- **Partial-running tone = neutral, unless there are real issues.** Don't cry wolf: a stopped
  app is often intentional. Map by health, aligned with StatusPill:
  - `all > 0 && running === all` → **ok / green** ("all up").
  - `counts.issues > 0` → **warn / amber** (something actually wrong — crashed/degraded/failed).
  - some stopped, no issues (`running < all`, `issues === 0`) → **muted / neutral** (intentional).
  - `all === 0` → **muted / neutral** ("0/0", no apps yet).
  - **Never `err`/red** for occupancy — red is reserved for the over-commit warning and the
    real utilisation meters.
- **Add a proper `info` (blue) semantic family**, not a one-off. Mirror the `ok/warn/err`
  shape (`{base, -fg, -bg, -border}`, light + dark) so the new `Badge` `info` variant matches
  `success` exactly and is reusable (plan 20's VAC-system-DB badge + unbudgeted notice both want
  it). Reuse the existing brand-blue hue (~258 in oklch) so it sits in-palette with `--brand`.
  Keep `brand` (solid button fill) and `info` (soft badge surface) distinct rather than
  overloading `brand`.
- **Over-commit warning stays as-is (red text).** Optionally it could also become a
  `destructive` badge for visual symmetry, but that's out of scope here — keep severity ordering
  red > blue and the existing ternary precedence.

## Implementation

Four files. Steps 1–2 are the shared `info` dependency (coordinate with plan 20 — land once);
steps 3–4 are this card.

### 1. `ui/src/index.css` — add the `info` colour family

Mirror the `err` block. In the light `:root` (near `index.css:135-138`) and `.dark`
(near `:192-195`) sections add an `info` family, and bind it under `@theme inline`
(near `:73-77`). Suggested starting values (blue, hue ≈ 258, matching the lightness/chroma
cadence of the other families — final values are a design-token call):

```css
/* light :root */
--info:        oklch(0.55 0.16 258);
--info-fg:     oklch(0.42 0.14 258);
--info-bg:     oklch(0.97 0.03 258);
--info-border: oklch(0.86 0.08 258);

/* .dark */
--info:        oklch(0.65 0.15 258);
--info-fg:     oklch(0.85 0.12 258);
--info-bg:     oklch(0.28 0.07 258);
--info-border: oklch(0.38 0.10 258);

/* @theme inline */
--color-info: var(--info);
--color-info-foreground: var(--info-fg);
--color-info-bg: var(--info-bg);
--color-info-border: var(--info-border);
```

This yields `bg-info-bg`, `text-info-foreground`, `border-info-border`, `bg-info` utilities.

### 2. `ui/src/components/ui/badge.tsx` — add the `info` variant

Add one line to `badgeVariants`, parallel to `success`:

```ts
info: 'bg-info-bg text-info-foreground [a&]:hover:bg-info-bg/80',
```

(Border-transparent like the others; `[&>svg]:size-3` already styles a leading icon.)

While here, **also add a `warn` variant** for step 3's amber case (symmetric, one line):

```ts
warn: 'bg-warn-bg text-warn-foreground [a&]:hover:bg-warn-bg/80',
```

### 3. `ui/src/features/apps/apps-dashboard.tsx` — occupancy badge for "Running apps"

Replace the `<BudgetRow label="Running apps" … />` (`:203-208`) with a dedicated occupancy row
that keeps the existing label/value layout but swaps the bar for a health-toned badge. Add a
small helper rather than overloading `BudgetRow` (its semantics — a meter toward a cap — no
longer apply):

```tsx
function appsBadgeVariant(c: { all: number; running: number; issues: number }) {
  if (c.all > 0 && c.running === c.all) return 'success' as const // all up → green
  if (c.issues > 0) return 'warn' as const                         // something broken → amber
  return 'secondary' as const                                      // some stopped / none → neutral
}

function RunningAppsRow({ running, all, issues }: { running: number; all: number; issues: number }) {
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-muted-foreground">Running apps</span>
      <Badge variant={appsBadgeVariant({ running, all, issues })} className="font-mono tabular-nums">
        {running}/{all}
      </Badge>
    </div>
  )
}
```

Mount it in place of the old row (`:203-208`):

```tsx
<RunningAppsRow running={counts.running} all={counts.all} issues={counts.issues} />
```

Tone mapping: `success` (green) for all-up, `warn` (amber) for real issues, `secondary` (the
existing muted grey badge) for intentional-stops / empty. Render `{running}/{all}` so the figure
stays visible. Leave the three `BudgetRow`/`Meter` rows below (Host RAM, Disk, Allocated RAM)
**unchanged** — their red-at-80% is correct, and `BudgetRow`/`Meter` stay imported for them.

### 4. `ui/src/features/apps/apps-dashboard.tsx` — info badge for the unbudgeted notice

Add imports: `Badge` (`@/components/ui/badge`) and `Info` from `lucide-react` (extend the
existing import at `:3`). Replace the muted `<p>` (`:238-244`) inside the existing ternary,
preserving the over-commit branch above it and the exact wording:

```tsx
{budget?.over_committed ? (
  <p className="mt-3 text-2xs text-err">
    Apps have reserved more RAM than the box has — over-committed.
  </p>
) : budget && budget.apps_total > budget.apps_with_limit ? (
  <Badge variant="info" className="mt-3 text-2xs">
    <Info className="size-3" aria-hidden />
    {budget.apps_total - budget.apps_with_limit} app
    {budget.apps_total - budget.apps_with_limit === 1 ? '' : 's'} without a RAM limit
    aren&apos;t budgeted.
  </Badge>
) : null}
```

Icon is decorative (`aria-hidden`); the text carries meaning. Ordering (red over-commit branch
first) is unchanged, so severity reads red > blue.

### Optional follow-on (not required)

- Add an `info` tone to `StatusPill` (`status-pill.tsx`) if any status ever wants blue — not
  needed for this card, but cheap once the token family exists.
- Promote the over-commit `<p className="text-err">` to a `Badge variant="destructive"` for
  visual symmetry with the new info badge. Defer unless the card looks inconsistent.

## Edge cases

- **`all === 0`** (no apps): badge shows `0/0`, neutral tone — not green (avoid "all up" when
  there's nothing up) and not red. The `total={counts.all || 1}` guard in the old code existed
  only to avoid divide-by-zero in the meter; the badge has no division, so the `|| 1` goes away.
- **Some stopped, none broken** (operator stopped an app on purpose): neutral, not amber — the
  "don't cry wolf" case the strategy calls for.
- **Loading state:** `apps` may be `undefined` → `list` is `[]` → `counts` all-zero → neutral
  `0/0`. Acceptable; matches the existing skeleton/empty handling.
- **`budget` undefined / `apps_total === apps_with_limit`:** notice doesn't render (unchanged).

## Testing

- **Unit (vitest, tests sit next to code):** add a test for `appsBadgeVariant` (pure function) —
  all-up → success, issues>0 → warn, partial-stopped → secondary, empty → secondary. Mirrors the
  existing `status-filter.test.ts` pattern (export the helper, or colocate a tiny test file).
- **a11y:** the budget card is exercised by `src/test/a11y.test.tsx` / `smoke.test.tsx`; removing
  the running-apps `Meter` drops one `progressbar` but the fraction remains as text — confirm
  those suites still pass. The info `Badge`'s icon is `aria-hidden`, text is the accessible name —
  no unnamed-control regressions.
- **Manual / `/run`:** load the apps dashboard with (a) all apps running → green `N/N`; (b) one
  crashed → amber; (c) one intentionally stopped → neutral; (d) an app with no RAM limit → blue
  info badge below any red over-commit line.
- Run `make typecheck` + `make lint` (new Badge variants; `Meter` import stays — still used by the
  RAM/disk `BudgetRow`s).

## Coordination with plan 20

The `info` colour family (step 1) and `Badge info` variant (step 2) are a **shared dependency**
with **20 — Database section** (its VAC-system-DB badge wants the same blue;
`20-database-section.md:209-210`). Whichever plan ships first lands those two steps; the other
just consumes them. Keep them in one commit/PR if the two plans land together.

## Acceptance

On the apps dashboard:
- With all apps running, the running-apps figure reads **green** (`N/N`), never red.
- With an app crashed/degraded it reads **amber**; with an app merely stopped it reads
  **neutral** — never falsely alarming.
- The three RAM/disk rows keep their meters and still turn red past 80% (unchanged).
- The "without a RAM limit aren't budgeted" notice renders as a **blue info badge with an info
  icon**, sitting **below** any red over-commit warning (severity red > blue preserved).
- `make typecheck`, `make lint`, and the vitest suite pass.
