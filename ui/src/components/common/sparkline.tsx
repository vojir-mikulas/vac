import { useId } from 'react'
import { Area, AreaChart, ResponsiveContainer, Tooltip, YAxis } from 'recharts'

import { cn } from '@/lib/utils'

// Compact trend sparkline built on recharts (same engine as the full charts), so
// live updates tween between samples instead of snapping. A gradient area under a
// monotone line; no axes or grid — the tile's numeric value is the headline
// readout. Decorative by default (aria-hidden); pass `ariaLabel` to expose it.
//
// Pass `formatValue` to opt into a hover tooltip: it reads out the value under
// the cursor (the tile only shows the latest/aggregate, so per-point values are
// otherwise invisible).
//
// `animate` drives the morph: leave it on for live series (CPU) so each new
// sample eases in; the duration is tuned a touch under the poll interval so the
// line settles before the next point arrives.
export function Sparkline({
  data,
  color = 'var(--color-brand)',
  className,
  ariaLabel,
  animate = true,
  formatValue,
}: {
  data: number[]
  color?: string
  className?: string
  ariaLabel?: string
  animate?: boolean
  formatValue?: (value: number) => string
}) {
  const gradientId = useId().replace(/:/g, '')
  const points = data.filter((n) => Number.isFinite(n))

  // Nothing to trend yet (buffer still filling) — a faint baseline reads better
  // than recharts' empty state or a lone dot.
  if (points.length < 2) {
    return (
      <div
        className={cn('flex h-full w-full items-center', className)}
        aria-hidden={ariaLabel ? undefined : true}
        role={ariaLabel ? 'img' : undefined}
        aria-label={ariaLabel}
      >
        <div className="h-px w-full" style={{ backgroundColor: color, opacity: 0.3 }} />
      </div>
    )
  }

  const chartData = points.map((v, i) => ({ i, v }))
  // Pad the domain so a flat-ish series isn't pinned to the edges and tiny
  // wiggles stay legible.
  const min = Math.min(...points)
  const max = Math.max(...points)
  const pad = (max - min || 1) * 0.15

  return (
    <div
      className={cn('h-full w-full', className)}
      role={ariaLabel ? 'img' : undefined}
      aria-label={ariaLabel}
      aria-hidden={ariaLabel ? undefined : true}
    >
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 120, height: 28 }}>
        <AreaChart data={chartData} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.28} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <YAxis hide domain={[min - pad, max + pad]} />
          {formatValue ? (
            <Tooltip
              cursor={{ stroke: color, strokeOpacity: 0.4, strokeWidth: 1 }}
              isAnimationActive={false}
              content={<SparklineTooltip color={color} formatValue={formatValue} />}
            />
          ) : null}
          <Area
            dataKey="v"
            type="monotone"
            stroke={color}
            strokeWidth={1.5}
            fill={`url(#${gradientId})`}
            dot={false}
            activeDot={formatValue ? { r: 2.5, strokeWidth: 0, fill: color } : false}
            isAnimationActive={animate}
            animationDuration={900}
            animationEasing="ease-out"
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}

// Bare value readout for the hovered point — no label/axis, since a sparkline has
// no meaningful x-axis to caption. Mirrors the full chart tooltip's surface.
function SparklineTooltip({
  active,
  payload,
  color,
  formatValue,
}: {
  active?: boolean
  payload?: Array<{ value?: number }>
  color: string
  formatValue: (value: number) => string
}) {
  const value = payload?.[0]?.value
  if (!active || typeof value !== 'number') return null
  return (
    <div className="flex items-center gap-1.5 rounded-md border border-border/50 bg-background px-2 py-1 text-2xs shadow-xl">
      <span
        className="size-1.5 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
        aria-hidden
      />
      <span className="font-mono font-medium tabular-nums">{formatValue(value)}</span>
    </div>
  )
}
