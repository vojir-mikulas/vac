import { useTranslation } from 'react-i18next'
import { Cell, Pie, PieChart, Sector } from 'recharts'

import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart'
import type { DiskUsage } from '@/lib/api/instance'
import { formatBytes } from '@/lib/format'

// Slice colors are shared with the legend rows in storage-page so a row and its
// wedge always read as the same thing. Keyed by the host.* translation key.
export const HOST_COLORS = {
  images: 'var(--chart-1)',
  containers: 'var(--chart-2)',
  volumes: 'var(--chart-3)',
  buildCache: 'var(--chart-4)',
} as const

type Slice = {
  key: keyof typeof HOST_COLORS
  label: string
  size: number
  reclaimable: number
  fill: string
}

// Grow the hovered wedge slightly so the active slice is obvious without a legend
// highlight. Recharts hands the active shape the sector's full geometry.
function renderActiveShape(props: React.ComponentProps<typeof Sector>) {
  return <Sector {...props} outerRadius={(props.outerRadius ?? 0) + 6} />
}

function SliceTooltip({ active, payload }: { active?: boolean; payload?: { payload: Slice }[] }) {
  const { t } = useTranslation('storage')
  const slice = payload?.[0]?.payload
  if (!active || !slice) return null
  return (
    <div className="rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <div className="flex items-center gap-2 font-medium">
        <span className="size-2.5 rounded-[2px]" style={{ background: slice.fill }} />
        {slice.label}
      </div>
      <div className="mt-1 font-mono tabular-nums">{formatBytes(slice.size)}</div>
      {slice.reclaimable > 0 ? (
        <div className="text-muted-foreground">
          {t('host.reclaimable', { size: formatBytes(slice.reclaimable) })}
        </div>
      ) : null}
    </div>
  )
}

export function HostDiskChart({ host, className }: { host: DiskUsage; className?: string }) {
  const { t } = useTranslation('storage')

  const entries: {
    key: keyof typeof HOST_COLORS
    label: string
    entry: DiskUsage[keyof DiskUsage]
  }[] = [
    { key: 'images', label: t('host.images'), entry: host.images },
    { key: 'containers', label: t('host.containers'), entry: host.containers },
    { key: 'volumes', label: t('host.volumes'), entry: host.volumes },
    { key: 'buildCache', label: t('host.buildCache'), entry: host.build_cache },
  ]

  const total = entries.reduce((sum, e) => sum + e.entry.size_bytes, 0)
  // Zero-byte categories would render as invisible wedges that still steal hover
  // targets — drop them from the donut. The legend rows beside it list all four.
  const slices: Slice[] = entries
    .filter((e) => e.entry.size_bytes > 0)
    .map((e) => ({
      key: e.key,
      label: e.label,
      size: e.entry.size_bytes,
      reclaimable: e.entry.reclaimable_bytes,
      fill: HOST_COLORS[e.key],
    }))

  const chartConfig = Object.fromEntries(
    entries.map((e) => [e.key, { label: e.label, color: HOST_COLORS[e.key] }]),
  ) satisfies ChartConfig

  if (total === 0) {
    return (
      <div
        className={`grid aspect-square place-items-center rounded-full border border-dashed text-xs text-muted-foreground ${className ?? ''}`}
      >
        {t('host.empty')}
      </div>
    )
  }

  return (
    <div className={`relative ${className ?? ''}`}>
      {/* Decorative for assistive tech — the legend rows are the text equivalent. */}
      <ChartContainer config={chartConfig} className="aspect-square w-full" aria-hidden="true">
        <PieChart>
          <ChartTooltip content={<SliceTooltip />} wrapperStyle={{ zIndex: 50 }} />
          <Pie
            data={slices}
            dataKey="size"
            nameKey="label"
            cx="50%"
            cy="50%"
            innerRadius="56%"
            outerRadius="78%"
            paddingAngle={2}
            stroke="var(--card)"
            strokeWidth={2}
            activeShape={renderActiveShape}
          >
            {slices.map((s) => (
              <Cell key={s.key} fill={s.fill} />
            ))}
          </Pie>
        </PieChart>
      </ChartContainer>
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
        <span className="font-mono text-sm font-semibold tabular-nums">{formatBytes(total)}</span>
        <span className="text-[11px] text-muted-foreground">{t('host.used')}</span>
      </div>
    </div>
  )
}
