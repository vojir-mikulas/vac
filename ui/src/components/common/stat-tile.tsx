import { cn } from '@/lib/utils'

// Horizontal strip of stat tiles separated by 1px hairlines (the `gap-px` over
// a bordered, border-colored grid reproduces the prototype's divider look).
// Collapses 4 → 2 → 1 columns.
export function StatStrip({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-px overflow-hidden rounded-xl border bg-border sm:grid-cols-2 lg:grid-cols-4">
      {children}
    </div>
  )
}

export function StatTile({
  label,
  value,
  sub,
  accent,
}: {
  label: string
  value: React.ReactNode
  sub?: React.ReactNode
  accent?: boolean
}) {
  return (
    <div className="flex flex-col gap-1 bg-background px-4.5 py-4">
      <div className="text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div
        className={cn(
          'font-sans text-2xl font-semibold tabular-nums tracking-tight',
          accent ? 'text-brand' : 'text-foreground',
        )}
      >
        {value}
      </div>
      {sub ? <div className="text-2xs text-muted-foreground">{sub}</div> : null}
    </div>
  )
}
