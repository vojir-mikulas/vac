import { cn } from '@/lib/utils'

// Thin progress meter used in the sidebar (host vitals) and budget cards.
// Turns red past 80% utilisation, matching the prototype.
export function Meter({
  pct,
  className,
  tone = 'fg',
}: {
  pct: number
  className?: string
  tone?: 'fg' | 'brand'
}) {
  const clamped = Math.min(100, Math.max(0, pct))
  const over = clamped > 80
  const fill = over ? 'bg-err' : tone === 'brand' ? 'bg-brand' : 'bg-foreground'
  return (
    <div className={cn('overflow-hidden rounded-full bg-surface-2', className)}>
      <div
        className={cn('h-full transition-[width] duration-200', fill)}
        style={{ width: `${clamped}%` }}
      />
    </div>
  )
}
