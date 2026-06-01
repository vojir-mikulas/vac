import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import { isDeployActive } from '@/lib/deploy-status'
import type { Deployment, DeploymentLogLine } from '@/types/api'

export const deploymentsApi = {
  list: (appId: string) => api.get<Deployment[]>(`apps/${appId}/deployments`),
  get: (appId: string, did: string) => api.get<Deployment>(`apps/${appId}/deployments/${did}`),
  trigger: (appId: string) => api.post<Deployment>(`apps/${appId}/deployments`),
  rollback: (appId: string, did: string) =>
    api.post<Deployment>(`apps/${appId}/deployments/${did}/rollback`),
  logs: (appId: string, did: string, after?: number, limit = 500) => {
    const params = new URLSearchParams()
    if (after != null) params.set('after', String(after))
    params.set('limit', String(limit))
    return api.get<DeploymentLogLine[]>(
      `apps/${appId}/deployments/${did}/logs?${params.toString()}`,
    )
  },
}

export function useDeployments(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.deployments(appId),
    queryFn: () => deploymentsApi.list(appId),
    // Poll while a deployment is mid-pipeline so its status advances live.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((d) => isDeployActive(d.status)) ? 3_000 : false,
  })
}

export function useDeployment(appId: string, did: string) {
  return useQuery({
    queryKey: queryKeys.apps.deployment(appId, did),
    queryFn: () => deploymentsApi.get(appId, did),
  })
}

// useSettleAfterDeploy refreshes the views a new deployment changes: its own
// history list and the app-detail summary. Shared by the manual-deploy and
// rollback mutations so their post-deploy refresh stays in lockstep.
function useSettleAfterDeploy(appId: string) {
  const qc = useQueryClient()
  return () => {
    qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
    qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
  }
}

export function useTriggerDeploy(appId: string) {
  return useMutation({
    mutationFn: () => deploymentsApi.trigger(appId),
    onSuccess: useSettleAfterDeploy(appId),
  })
}

// useRollbackDeploy re-deploys the commit of a prior successful deployment. The
// new deployment appears at the top of the history and streams logs like any
// other deploy.
export function useRollbackDeploy(appId: string) {
  return useMutation({
    mutationFn: (did: string) => deploymentsApi.rollback(appId, did),
    onSuccess: useSettleAfterDeploy(appId),
  })
}
