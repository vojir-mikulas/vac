# VAC — UI Motion (Framer Motion) Plan

> Introduces subtle, consistent motion to the React SPA in [`ui/`](../../ui) using
> **Framer Motion** (the maintained `motion` package). Goal: a fluid, calm dashboard
> with no "jumpy" pops on load, route change, filter, or data update.
> **Scope: UI animation only** — no behavior, data, or layout-structure changes.

---

## 1. Goal & Scope

Make the dashboard feel fluid and clean. Content should fade/settle into place rather
than pop; route changes, list filtering, and skeleton→content swaps should transition
smoothly; interactive surfaces get tasteful micro-feedback. Everything stays **subtle**
(short durations, small distances, soft easing) and **honors reduced-motion**.

### In scope
- Framer Motion runtime wired into the app root (`LazyMotion` + `MotionConfig`).
- A single motion-token module (`ui/src/lib/motion.ts`) so timings/easings/variants are
  consistent everywhere — no ad-hoc durations.
- Route/page transitions, skeleton→content cross-fades, list entrance + reorder,
  micro-interactions, animated meter/stat values, sidebar active-nav indicator,
  banner/toast collapse.

### Out of scope (for now)
- Overlay animations (dialog, sheet, popover, dropdown) — already animated via Radix
  `data-state` + `tw-animate-css`; left as-is to avoid double-animating.
- Any non-visual change (data flow, routing structure, component APIs).
- Large/long/bouncy motion, parallax, scroll-driven effects.

---

## 2. Current state (what we're fixing)

- `tw-animate-css` is present; Radix overlays already animate. Content does not.
- `index.css` already collapses transitions/animations under `prefers-reduced-motion`
  — **this contract must be preserved.**
- The `_app` layout swaps pages through `<Outlet />` with **no transition**.
- Tables/cards/stat-tiles render instantly (pop in); skeleton→content is an instant swap.
- Meters and stat numbers jump straight to their value.
- Live data (WS deployments, metrics, logs) re-renders frequently — animation here must
  be cheap and must **not** re-trigger on every tick.

---

## 3. Foundation

- **Install `motion`** (current package name; `framer-motion` is the legacy alias,
  `motion/react` is the maintained import). Add to `ui/package.json`.
- Use **`LazyMotion` + `domAnimation`** at the app root and `m.*` components instead of
  the full `motion.*` import — keeps the added bundle small (~5kb vs ~34kb).
- Wrap the app in **`<MotionConfig reducedMotion="user">`** so all motion auto-disables
  under OS reduce-motion. Complements (does not replace) the existing CSS override.
- Create **`ui/src/lib/motion.ts`** as the single source of truth:
  - durations — `fast: 0.15`, `base: 0.22`, `slow: 0.35`
  - easings — soft `easeOut` cubic-bezier for entrances, `easeInOut` for layout
  - variants — `fade`, `fadeUp`, `staggerContainer`, `listItem`
  - distances kept to **4–8px** only (never large slides — this is what keeps it clean).

---

## 4. Workstreams

### 4.1 Route / page transitions (biggest win)
Wrap `<Outlet />` in the `_app` layout with a `<PageTransition>` keyed on route path,
inside `<AnimatePresence mode="wait">`. Outgoing page fades out (~120ms); incoming fades
up 6px (~220ms). No horizontal slide (avoids the jumpy feel).

### 4.2 Skeleton → content cross-fade
Replace instant `isLoading ? <Skeleton/> : <Content/>` swaps with an `AnimatePresence`
cross-fade so loaded data doesn't pop. Centralize as one small helper (`<SwapFade>` /
`<Reveal>`) and apply across apps, app-detail, deployments, database, etc.

### 4.3 List & table entrance + reorder
- Stagger row entrance on mount (~30ms stagger), capped to the first ~12 rows so long
  lists don't cascade forever; the rest render instantly.
- Wrap filterable lists (apps table, deployments, activity) with `layout` animations +
  `AnimatePresence` so filtering/reordering glides rows to position instead of snapping.

### 4.4 Micro-interactions
- `whileHover` / `whileTap` lift on clickable cards (app rows, stat tiles, addon cards):
  subtle scale `1.0 → 1.01` + shadow, fast.
- Buttons: gentle `whileTap` scale (~0.97). Tiny by design.

### 4.5 Animated values (no number-snapping)
- `meter.tsx` animates fill width (spring/tween) instead of jumping.
- Stat tiles / budget numbers: optional count-up **on first mount only** — skip on WS
  updates to avoid constant motion.

### 4.6 Layout polish
- Sidebar active-nav indicator: shared `layoutId` pill that glides between items.
- Banners (insecure-HTTP) and toasts: height/opacity collapse on dismiss via
  `AnimatePresence` instead of vanishing.

---

## 5. Guardrails (the "subtle & clean" contract)

- Animate only `transform` + `opacity` (GPU-friendly, no layout thrash), except the
  deliberate `layout`/meter cases.
- Short durations, small distances, `easeOut` — nothing bouncy or long.
- Keep Radix/`tw-animate-css` for overlays; Framer handles content/layout/route only.
- All motion flows through `lib/motion.ts` tokens — no inline magic numbers.
- Reduced-motion: enforced by `MotionConfig reducedMotion="user"` **and** the existing
  CSS override.

---

## 6. Sequencing

1. **Phase 0 + 1** — foundation (§3) + route transitions (§4.1) as one reviewable commit.
   This alone removes most of the jumpiness.
2. **Phase 2** — skeleton cross-fades (§4.2) + list entrance/reorder (§4.3).
3. **Phase 3** — micro-interactions (§4.4) + animated values (§4.5).
4. **Phase 4** — layout polish (§4.6).

---

## 7. Verification

- `make typecheck` + `make lint`.
- Run `make dev` and eyeball: dashboard load, route changes, app filtering, dialogs.
- Toggle OS **Reduce Motion ON** → confirm everything is instant (MotionConfig contract).
- Spot-check bundle delta from `motion` is small (LazyMotion in effect).
