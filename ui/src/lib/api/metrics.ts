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
