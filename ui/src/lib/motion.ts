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

export const fadeUp: Variants = {
  hidden: { opacity: 0, y: RISE },
  visible: { opacity: 1, y: 0, transition: transition.base },
  exit: { opacity: 0, y: -RISE, transition: transition.fast },
}

// Stagger container for lists. Children use `listItem`. Keep the stagger short so
// long lists don't cascade forever — callers should cap how many rows opt in.
export const staggerContainer: Variants = {
  hidden: {},
  visible: {
    transition: { staggerChildren: 0.03 },
  },
}

export const listItem: Variants = {
  hidden: { opacity: 0, y: RISE },
  visible: { opacity: 1, y: 0, transition: transition.base },
  exit: { opacity: 0, transition: transition.fast },
}
