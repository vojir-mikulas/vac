import { useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import { isDeployActive } from '@/lib/deploy-status'
import { useWebSocket } from '@/lib/ws/use-websocket'
import type { ActiveDeployment, Deployment, DeploymentLogLine } from '@/types/api'

export const deploymentsApi = {
  list: (appId: string) => api.get<Deployment[]>(`apps/${appId}/deployments`),
  get: (appId: string, did: string) => api.get<Deployment>(`apps/${appId}/deployments/${did}`),
  trigger: (appId: string) => api.post<Deployment>(`apps/${appId}/deployments`),
  rollback: (appId: string, did: string) =>
    api.post<Deployment>(`apps/${appId}/deployments/${did}/rollback`),
  cancel: (appId: string, did: string) =>
    api.post<{ status: string }>(`apps/${appId}/deployments/${did}/cancel`),
  // Instance-wide queue: running + queued across all apps, FIFO order.
  listActive: () => api.get<ActiveDeployment[]>('deployments/active'),
  logs: (appId: string, did: string, after?: number, limit = 500) => {
    const params = new URLSearchParams()
    if (after != null) params.set('after', String(after))
    params.set('limit', String(limit))
    return api.get<DeploymentLogLine[]>(
      `apps/${appId}/deployments/${did}/logs?${params.toString()}`,
    )
  },
}

// useActiveDeployments backs the deploy queue and the sidebar badge. It polls as
// a fallback; useActiveDeploymentsStream pushes live snapshots into this same
// query cache for near-instant updates.
export function useActiveDeployments(enabled = true) {
  return useQuery({
    queryKey: queryKeys.deployments.active,
    queryFn: () => deploymentsApi.listActive(),
    enabled,
    refetchInterval: (query) => ((query.state.data ?? []).length > 0 ? 5_000 : false),
  })
}

// useActiveDeploymentsStream subscribes to /deployments/stream and writes each
// snapshot straight into the active-deployments query cache. Mount it once high
// in the tree (the app shell) so the sidebar badge and the Deployments page both
// stay live regardless of which is on screen — one connection, paused while the
// tab is hidden.
export function useActiveDeploymentsStream() {
  const qc = useQueryClient()
  // App IDs that were mid-deploy in the previous snapshot. When one drops out of
  // the active set its deploy has settled (the snapshot never carries terminal
  // rows), so we refresh the views that the completion changes — otherwise the
  // app's status pill and domains stay frozen at their last value until the 30s
  // staleTime lapses or the tab refocuses.
  const prevActive = useRef<Set<string>>(new Set())
  useWebSocket('deployments/stream', {
    onFrame: (frame) => {
      if (frame.type !== 'deployments') return
      const rows = (frame.data as ActiveDeployment[]) ?? []
      qc.setQueryData(queryKeys.deployments.active, rows)

      const nowActive = new Set(rows.map((d) => d.app_id))
      const finished = [...prevActive.current].filter((id) => !nowActive.has(id))
      prevActive.current = nowActive
      if (finished.length === 0) return

      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
      for (const appId of finished) {
        qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
        qc.invalidateQueries({ queryKey: queryKeys.apps.services(appId) })
        qc.invalidateQueries({ queryKey: queryKeys.apps.domains(appId) })
        qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
      }
    },
  })
}

// useCancelDeployment stops a queued or in-flight deploy. It refreshes the queue
// panel plus the app's own deployment history/summary.
export function useCancelDeployment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ appId, did }: { appId: string; did: string }) =>
      deploymentsApi.cancel(appId, did),
    onSuccess: (_r, { appId }) => {
      qc.invalidateQueries({ queryKey: queryKeys.deployments.active })
      qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
    },
  })
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
// history list, the app-detail summary, and its domains (auto hosts only exist
// once the deploy has created services, so a freshly-created app shows none
// until this fires). Shared by the manual-deploy and rollback mutations so their
// post-deploy refresh stays in lockstep.
function useSettleAfterDeploy(appId: string) {
  const qc = useQueryClient()
  return () => {
    qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
    qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
    qc.invalidateQueries({ queryKey: queryKeys.apps.domains(appId) })
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
