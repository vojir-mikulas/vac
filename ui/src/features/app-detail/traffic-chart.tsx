import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'

import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useAppMetrics } from '@/lib/api/metrics'

const RANGES = [
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
] as const

function formatTick(ts: string, locale: string): string {
  const d = new Date(ts)
  return d.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })
}

export function TrafficChart({ appId }: { appId: string }) {
  const { t, i18n } = useTranslation('app-detail')
  const locale = i18n.resolvedLanguage ?? 'en'
  const [range, setRange] = useState<string>('6h')
  const { data, isLoading } = useAppMetrics(appId, range)

  const chartConfig = {
    requests: { label: t('traffic.requests'), color: 'var(--chart-2)' },
    errors: { label: t('traffic.errors'), color: 'var(--chart-1)' },
  } satisfies ChartConfig

  return (
    <div className="rounded-xl border p-5">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-sm font-medium">{t('traffic.title')}</h2>
        <div className="flex gap-1">
          {RANGES.map((r) => (
            <button
              key={r.value}
              type="button"
              onClick={() => setRange(r.value)}
              className={cn(
                'cursor-pointer rounded-md px-2 py-1 text-xs font-medium transition-colors',
                range === r.value
                  ? 'bg-surface-2 text-foreground'
                  : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      {isLoading ? (
        <Skeleton className="h-56 w-full" />
      ) : !data || data.length === 0 ? (
        <div className="grid h-56 place-items-center text-sm text-muted-foreground">
          {t('traffic.empty')}
        </div>
      ) : (
        <figure className="m-0">
          {/* The SVG is decorative for assistive tech; the sr-only table below
              is the text equivalent (charts are deferred — this is the graceful
              degrade). */}
          <ChartContainer config={chartConfig} className="h-56 w-full" aria-hidden="true">
            <AreaChart data={data} margin={{ left: 4, right: 4, top: 4 }}>
              <defs>
                <linearGradient id="fillRequests" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-requests)" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="var(--color-requests)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} strokeDasharray="3 3" />
              <XAxis
                dataKey="ts"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                minTickGap={32}
                tickFormatter={(ts) => formatTick(ts, locale)}
              />
              <YAxis tickLine={false} axisLine={false} width={32} />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    labelFormatter={(_, p) => formatTick(p?.[0]?.payload?.ts, locale)}
                  />
                }
              />
              <Area
                dataKey="requests"
                type="monotone"
                stroke="var(--color-requests)"
                fill="url(#fillRequests)"
                strokeWidth={2}
              />
              <Area
                dataKey="errors"
                type="monotone"
                stroke="var(--color-errors)"
                fill="transparent"
                strokeWidth={2}
              />
            </AreaChart>
          </ChartContainer>
          <table className="sr-only">
            <caption>{t('traffic.caption')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('traffic.time')}</th>
                <th scope="col">{t('traffic.requests')}</th>
                <th scope="col">{t('traffic.errors')}</th>
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.ts}>
                  <td>{formatTick(d.ts, locale)}</td>
                  <td>{d.requests}</td>
                  <td>{d.errors}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </figure>
      )}
    </div>
  )
}
