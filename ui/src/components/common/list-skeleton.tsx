import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// Skeleton that mirrors a bordered "list card" — an optional header bar plus
// stacked rows — so it occupies roughly the same footprint and chrome as the
// loaded list. Under SwapFade the card outline stays put and only the rows fade
// in, which keeps the swap from jumping. `rows` ≈ how many show above the fold.
export function ListSkeleton({
  rows = 4,
  header = false,
  avatar = false,
  className,
}: {
  rows?: number
  header?: boolean
  avatar?: boolean
  className?: string
}) {
  return (
    <Card className={cn('gap-0 overflow-hidden p-0', className)}>
      {header ? (
        <div className="border-b bg-surface-1 px-5 py-2.5">
          <Skeleton className="h-3 w-28" />
        </div>
      ) : null}
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className={cn('flex items-center gap-3 px-5 py-3.5', (header || i > 0) && 'border-t')}
        >
          {avatar ? <Skeleton className="size-7 shrink-0 rounded-md" /> : null}
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3.5 w-2/5" />
            <Skeleton className="h-2.5 w-3/5" />
          </div>
          <Skeleton className="h-7 w-16 shrink-0 rounded-md" />
        </div>
      ))}
    </Card>
  )
}
