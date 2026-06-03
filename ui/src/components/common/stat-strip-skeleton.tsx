import { Skeleton } from '@/components/ui/skeleton'

// Skeleton matching a StatStrip — the bordered, hairline-divided row of stat tiles.
// Same grid/breakpoints as the real strip so it occupies the identical footprint.
export function StatStripSkeleton({ tiles = 4 }: { tiles?: number }) {
  return (
    <div className="grid grid-cols-1 gap-px overflow-hidden rounded-xl border bg-border sm:grid-cols-2 lg:grid-cols-4">
      {Array.from({ length: tiles }).map((_, i) => (
        <div key={i} className="flex flex-col gap-2 bg-background px-4.5 py-4">
          <Skeleton className="h-2.5 w-16" />
          <Skeleton className="h-6 w-20" />
          <Skeleton className="h-2.5 w-24" />
        </div>
      ))}
    </div>
  )
}
