import { m, type HTMLMotionProps } from 'motion/react'

import { cn } from '@/lib/utils'
import { hoverLift } from '@/lib/motion'

// A Card that lifts on hover and gives on press — for cards that are themselves
// interactive (catalog tiles, selectable items). Mirrors the base styling of
// <Card> so it's a drop-in where the lift is wanted; pass `className` to override
// padding/gap exactly as with Card.
//
// Typed as HTMLMotionProps<'div'> rather than ComponentProps<'div'> so motion's
// own event/animation prop types don't clash with React's.
export function MotionCard({ className, ...props }: HTMLMotionProps<'div'>) {
  return (
    <m.div
      data-slot="card"
      className={cn(
        'flex flex-col gap-6 rounded-xl border bg-card py-6 text-card-foreground',
        className,
      )}
      {...hoverLift}
      {...props}
    />
  )
}
