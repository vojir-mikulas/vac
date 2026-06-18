import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Deployment } from '@/types/api'

// Approval gate (docs/plans/maintenance-mode-and-deploy-gates.md, Phase 4). A
// push matching an approval-gated trigger creates a `pending-approval` deploy
// that isn't enqueued until an operator approves it.

export const approvalsApi = {
  listPending: (appId: string) => api.get<Deployment[]>(`apps/${appId}/deployments/pending`),
  approve: (appId: string, did: string) =>
    api.post<Deployment>(`apps/${appId}/deployments/${did}/approve`),
  reject: (appId: string, did: string) =>
    api.post<Deployment>(`apps/${appId}/deployments/${did}/reject`),
}

export function usePendingDeployments(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.pendingApprovals(appId),
    queryFn: () => approvalsApi.listPending(appId),
  })
}

function useApprovalMutation(appId: string, action: (did: string) => Promise<Deployment>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (did: string) => action(did),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.pendingApprovals(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
    },
  })
}

export function useApproveDeployment(appId: string) {
  return useApprovalMutation(appId, (did) => approvalsApi.approve(appId, did))
}

export function useRejectDeployment(appId: string) {
  return useApprovalMutation(appId, (did) => approvalsApi.reject(appId, did))
}
