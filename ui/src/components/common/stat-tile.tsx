import type { LucideIcon } from 'lucide-react'

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

// Tone colours the value (and aligns with the badge palette): `brand` for the
// hero metric, `ok`/`warn`/`err` for health-toned figures. `accent` is the
// legacy alias for `brand`.
type Tone = 'brand' | 'ok' | 'warn' | 'err'

const toneClass: Record<Tone, string> = {
  brand: 'text-brand',
  ok: 'text-ok',
  warn: 'text-warn',
  err: 'text-err',
}

export function StatTile({
  label,
  value,
  sub,
  accent,
  tone,
  icon: Icon,
  chart,
  onClick,
  ariaLabel,
}: {
  label: string
  value: React.ReactNode
  sub?: React.ReactNode
  accent?: boolean
  tone?: Tone
  icon?: LucideIcon
  /** Optional full-bleed sparkline pinned to the bottom of the tile. */
  chart?: React.ReactNode
  /** When set the whole tile becomes a button (e.g. to jump to a filter). */
  onClick?: () => void
  ariaLabel?: string
}) {
  const resolvedTone = tone ?? (accent ? 'brand' : undefined)
  const content = (
    <>
      <div className="flex items-center justify-between gap-2">
        <span className="text-2xs font-medium uppercase tracking-wider text-muted-foreground">
          {label}
        </span>
        {Icon ? <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden /> : null}
      </div>
      <div
        className={cn(
          'font-sans text-2xl font-semibold tabular-nums tracking-tight',
          resolvedTone ? toneClass[resolvedTone] : 'text-foreground',
        )}
      >
        {value}
      </div>
      {sub ? <div className="text-2xs text-muted-foreground">{sub}</div> : null}
      {chart ? <div className="-mx-4.5 -mb-4 mt-auto h-9 pt-2">{chart}</div> : null}
    </>
  )

  const base = 'flex h-full flex-col gap-1 bg-background px-4.5 py-4'
  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        aria-label={ariaLabel}
        className={cn(
          base,
          'cursor-pointer text-left transition-colors hover:bg-surface-1 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
        )}
      >
        {content}
      </button>
    )
  }
  return <div className={base}>{content}</div>
}
