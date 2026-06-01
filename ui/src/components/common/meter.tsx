import { cn } from '@/lib/utils'

// Thin progress meter used in the sidebar (host vitals) and budget cards.
// Turns red past 80% utilisation, matching the prototype.
//
// Pass `label` (what it measures, e.g. "CPU") to expose it as a labelled
// progressbar to screen readers — the value and the over-threshold "high" state
// (otherwise conveyed by colour alone) are announced via aria-valuetext. Without
// a label the bar is treated as decorative (aria-hidden) so it doesn't surface
// as an unnamed progressbar where the value already sits beside it as text.
export function Meter({
  pct,
  className,
  tone = 'fg',
  label,
}: {
  pct: number
  className?: string
  tone?: 'fg' | 'brand'
  label?: string
}) {
  const clamped = Math.min(100, Math.max(0, pct))
  const over = clamped > 80
  const value = Math.round(clamped)
  const fill = over ? 'bg-err' : tone === 'brand' ? 'bg-brand' : 'bg-foreground'
  const a11y = label
    ? {
        role: 'progressbar' as const,
        'aria-label': label,
        'aria-valuenow': value,
        'aria-valuemin': 0,
        'aria-valuemax': 100,
        'aria-valuetext': over ? `${value}% — high` : `${value}%`,
      }
    : { 'aria-hidden': true }
  return (
    <div className={cn('overflow-hidden rounded-full bg-surface-2', className)} {...a11y}>
      <div
        className={cn('h-full transition-[width] duration-200', fill)}
        style={{ width: `${clamped}%` }}
      />
    </div>
  )
}
