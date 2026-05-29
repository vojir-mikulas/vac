import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Deployment, DeploymentLogLine } from '@/types/api'

export const deploymentsApi = {
  list: (appId: string) => api.get<Deployment[]>(`apps/${appId}/deployments`),
  get: (appId: string, did: string) => api.get<Deployment>(`apps/${appId}/deployments/${did}`),
  trigger: (appId: string) => api.post<Deployment>(`apps/${appId}/deployments`),
  logs: (appId: string, did: string, after?: number, limit = 500) => {
    const params = new URLSearchParams()
    if (after != null) params.set('after', String(after))
    params.set('limit', String(limit))
    return api.get<DeploymentLogLine[]>(
      `apps/${appId}/deployments/${did}/logs?${params.toString()}`,
    )
  },
}

const ACTIVE_STATUSES = new Set(['queued', 'cloning', 'building', 'deploying', 'health-checking'])

export function useDeployments(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.deployments(appId),
    queryFn: () => deploymentsApi.list(appId),
    // Poll while a deployment is mid-pipeline so its status advances live.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((d) => ACTIVE_STATUSES.has(d.status)) ? 3_000 : false,
  })
}

export function useDeployment(appId: string, did: string) {
  return useQuery({
    queryKey: queryKeys.apps.deployment(appId, did),
    queryFn: () => deploymentsApi.get(appId, did),
  })
}

export function useTriggerDeploy(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => deploymentsApi.trigger(appId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
    },
  })
}
