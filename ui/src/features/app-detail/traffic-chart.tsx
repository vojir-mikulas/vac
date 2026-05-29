import { useState } from 'react'
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

const chartConfig = {
  requests: { label: 'Requests', color: 'var(--chart-2)' },
  errors: { label: 'Errors', color: 'var(--chart-1)' },
} satisfies ChartConfig

function formatTick(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
}

export function TrafficChart({ appId }: { appId: string }) {
  const [range, setRange] = useState<string>('6h')
  const { data, isLoading } = useAppMetrics(appId, range)

  return (
    <div className="rounded-xl border p-5">
      <div className="mb-4 flex items-center justify-between">
        <h3 className="text-sm font-medium">Request traffic</h3>
        <div className="flex gap-1">
          {RANGES.map((r) => (
            <button
              key={r.value}
              type="button"
              onClick={() => setRange(r.value)}
              className={cn(
                'rounded-md px-2 py-1 text-xs font-medium transition-colors',
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
          No traffic recorded in this range.
        </div>
      ) : (
        <ChartContainer config={chartConfig} className="h-56 w-full">
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
              tickFormatter={formatTick}
            />
            <YAxis tickLine={false} axisLine={false} width={32} />
            <ChartTooltip
              content={
                <ChartTooltipContent labelFormatter={(_, p) => formatTick(p?.[0]?.payload?.ts)} />
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
      )}
    </div>
  )
}
