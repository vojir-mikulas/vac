// Single source of truth for UI motion. Everything animated in the app pulls its
// timings, easings, and variants from here so the whole dashboard feels consistent
// and deliberately *subtle* — short durations, tiny distances, soft easing. No
// component should hand-roll its own magic numbers.
//
// Reduced-motion is handled globally by <MotionConfig reducedMotion="user"> at the
// app root (and the CSS override in index.css), so variants here don't need to
// special-case it.

import type { Transition, Variants } from 'motion/react'

// Durations (seconds). Keep these small — anything longer reads as sluggish.
export const duration = {
  fast: 0.15,
  base: 0.22,
  slow: 0.35,
} as const

// Easings. `easeOut` for entrances (decelerate into place), `easeInOut` for layout
// moves (settle symmetrically). Cubic-bezier tuples render the same everywhere.
type Bezier = [number, number, number, number]
export const ease = {
  out: [0.22, 1, 0.36, 1] as Bezier,
  inOut: [0.65, 0, 0.35, 1] as Bezier,
}

// Vertical travel for "fade up" entrances. Deliberately tiny — a hint of motion,
// never a slide.
export const RISE = 6

export const transition = {
  base: { duration: duration.base, ease: ease.out },
  fast: { duration: duration.fast, ease: ease.out },
  layout: { duration: duration.base, ease: ease.inOut },
} satisfies Record<string, Transition>

// --- Variants --------------------------------------------------------------

export const fade: Variants = {
  hidden: { opacity: 0 },
  visible: { opacity: 1, transition: transition.base },
  exit: { opacity: 0, transition: transition.fast },
}

// How many leading rows get a staggered delay. Beyond this they all settle together,
// so a 200-row list never cascades for seconds.
const STAGGER_CAP = 12
const STAGGER_STEP = 0.03

// List container: just propagates `initial`/`animate` to its `listItem` children.
// Per-item delay is computed from `custom` (the row index) so it can be capped —
// pass `custom={i}` on each item.
export const listContainer: Variants = {
  hidden: {},
  visible: {},
}

export const listItem: Variants = {
  hidden: { opacity: 0, y: RISE },
  visible: (i: number = 0) => ({
    opacity: 1,
    y: 0,
    transition: { ...transition.base, delay: Math.min(i, STAGGER_CAP) * STAGGER_STEP },
  }),
}

// Hover-lift micro-interaction for interactive cards: a 2px rise on hover and a
// faint press on tap. Spread onto a motion element. Kept tiny on purpose — a hint
// of depth, not a bounce.
export const hoverLift = {
  whileHover: { y: -2 },
  whileTap: { scale: 0.99 },
  transition: transition.fast,
} as const
