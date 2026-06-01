import * as React from 'react'
import { Progress as ProgressPrimitive } from 'radix-ui'

import { cn } from '@/lib/utils'

// Radix Progress.Root already exposes role="progressbar" and
// aria-valuenow/min/max. It has no visible label of its own, so callers MUST
// pass an accessible name (aria-label or aria-labelledby) — otherwise it reads
// as an anonymous progressbar to screen readers.
function Progress({
  className,
  value,
  ...props
}: React.ComponentProps<typeof ProgressPrimitive.Root>) {
  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      className={cn('relative h-2 w-full overflow-hidden rounded-full bg-primary/20', className)}
      {...props}
    >
      <ProgressPrimitive.Indicator
        data-slot="progress-indicator"
        className="h-full w-full flex-1 bg-primary transition-all"
        style={{ transform: `translateX(-${100 - (value || 0)}%)` }}
      />
    </ProgressPrimitive.Root>
  )
}

export { Progress }
