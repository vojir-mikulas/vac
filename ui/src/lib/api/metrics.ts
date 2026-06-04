import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { BoxBudget, HostStats, MetricSample } from '@/types/api'

export const metricsApi = {
  app: (appId: string, since: string) =>
    api.get<MetricSample[]>(`apps/${appId}/metrics?since=${since}`),
  service: (appId: string, name: string, since: string) =>
    api.get<MetricSample[]>(`apps/${appId}/services/${name}/metrics?since=${since}`),
  host: () => api.get<HostStats>('host/stats'),
  budget: () => api.get<BoxBudget>('host/budget'),
  hostMetrics: (since: string) => api.get<MetricSample[]>(`host/metrics?since=${since}`),
}

export function useAppMetrics(appId: string, since: string) {
  return useQuery({
    queryKey: queryKeys.apps.metrics(appId, since),
    queryFn: () => metricsApi.app(appId, since),
  })
}

export function useHostStats() {
  return useQuery({
    queryKey: queryKeys.host.stats,
    queryFn: () => metricsApi.host(),
    refetchInterval: 5_000,
  })
}

export function useBoxBudget() {
  return useQuery({
    queryKey: queryKeys.host.budget,
    queryFn: () => metricsApi.budget(),
    refetchInterval: 5_000,
  })
}

// useHostMetrics backs the dashboard traffic sparkline: the box-wide request
// series summed across all apps, downsampled server-side to a few dozen points.
export function useHostMetrics(since: string) {
  return useQuery({
    queryKey: queryKeys.host.metrics(since),
    queryFn: () => metricsApi.hostMetrics(since),
    refetchInterval: 30_000,
  })
}

// useCpuHistory keeps a short client-side ring buffer of host CPU readings so the
// dashboard can draw a live trend. Host vitals are an instantaneous snapshot
// (no server-side history), so this samples the 5s poll of useHostStats — the
// buffer is live-only and resets when the dashboard unmounts. `maxPoints` * 5s
// is the visible window (default ~2 min).
export function useCpuHistory(maxPoints = 24): number[] {
  const { data, dataUpdatedAt } = useHostStats()
  const [history, setHistory] = useState<number[]>([])
  const lastStamp = useRef(0)
  useEffect(() => {
    if (!data || dataUpdatedAt === lastStamp.current) return
    lastStamp.current = dataUpdatedAt
    setHistory((prev) => [...prev, data.cpu_percent].slice(-maxPoints))
  }, [data, dataUpdatedAt, maxPoints])
  return history
}
