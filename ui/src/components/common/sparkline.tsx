import { useId } from 'react'

import { cn } from '@/lib/utils'

// Lightweight, dependency-free trend sparkline: a gradient area under a single
// stroked line, drawn in a fixed viewBox that stretches to fill its container
// (preserveAspectRatio="none"). The stroke stays a uniform 1.5px at any size via
// non-scaling-stroke, so it reads crisp in the small dashboard stat tiles.
//
// Decorative by default (aria-hidden) — the tile's numeric value is the text
// equivalent. Pass `ariaLabel` to expose it as a labelled image instead.
const VIEW_W = 100
const VIEW_H = 32
const PAD_Y = 3

export function Sparkline({
  data,
  color = 'var(--color-brand)',
  className,
  ariaLabel,
}: {
  data: number[]
  color?: string
  className?: string
  ariaLabel?: string
}) {
  const gradientId = useId()
  const points = data.filter((n) => Number.isFinite(n))

  // Geometry. With < 2 points there's nothing to trend yet, so we draw a flat
  // baseline (a hairline at mid-height) rather than an empty box while the
  // client-side buffer fills.
  let line: string
  let area: string | null = null
  if (points.length < 2) {
    const midY = VIEW_H / 2
    line = `M 0 ${midY} L ${VIEW_W} ${midY}`
  } else {
    const min = Math.min(...points)
    const max = Math.max(...points)
    const range = max - min || 1
    const usable = VIEW_H - PAD_Y * 2
    const x = (i: number) => round((i / (points.length - 1)) * VIEW_W)
    const y = (v: number) => round(VIEW_H - PAD_Y - ((v - min) / range) * usable)
    line = points.map((v, i) => `${i === 0 ? 'M' : 'L'} ${x(i)} ${y(v)}`).join(' ')
    area = `${line} L ${VIEW_W} ${VIEW_H} L 0 ${VIEW_H} Z`
  }

  return (
    <svg
      viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
      preserveAspectRatio="none"
      className={cn('h-full w-full overflow-visible', className)}
      role={ariaLabel ? 'img' : undefined}
      aria-label={ariaLabel}
      aria-hidden={ariaLabel ? undefined : true}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.28} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      {area ? <path d={area} fill={`url(#${gradientId})`} stroke="none" /> : null}
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
        opacity={points.length < 2 ? 0.35 : 1}
      />
    </svg>
  )
}

function round(n: number): number {
  return Math.round(n * 100) / 100
}
