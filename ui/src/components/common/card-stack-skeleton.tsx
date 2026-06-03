import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// Skeleton for a vertical stack of cards (service / database / backup cards, etc).
// Mirrors the real stack's gap and card radius so the loaded cards land where the
// skeletons were instead of resizing the swap. Tune `rowHeight`/`gap` to the cards
// it stands in for.
export function CardStackSkeleton({
  count = 2,
  rowHeight = 'h-32',
  gap = 'gap-4',
}: {
  count?: number
  rowHeight?: string
  gap?: string
}) {
  return (
    <div className={cn('flex flex-col', gap)}>
      {Array.from({ length: count }).map((_, i) => (
        <Skeleton key={i} className={cn('w-full rounded-xl', rowHeight)} />
      ))}
    </div>
  )
}
