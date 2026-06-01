import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// AuditEntry is one row of the activity feed (plan 11). The before-snapshot used
// for revert lives server-side only and is never sent to the client.
export interface AuditEntry {
  id: string
  actor_type: 'user' | 'api_token' | 'system' | 'anonymous'
  actor: string // resolved username, "" for system/anonymous
  action: string // "PUT /api/apps/{id}/env"
  target_type?: string
  target_id?: string
  summary?: string
  status_code: number
  revertable: boolean // true only when undoable AND not yet reverted
  reverted_at?: string
  created_at: string
}

export const auditApi = {
  list: (limit = 100) => api.get<AuditEntry[]>(`audit?limit=${limit}`),
  revert: (id: string) => api.post<{ reverted: string; summary: string }>(`audit/${id}/revert`),
}

export function useActivity(limit = 100) {
  return useQuery({
    queryKey: queryKeys.activity,
    queryFn: () => auditApi.list(limit),
    refetchInterval: 30_000,
  })
}

export function useRevertActivity() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => auditApi.revert(id),
    onSuccess: () => {
      // The feed itself changes (entry now reverted + a new revert entry), and
      // the reverted resource's own queries may be stale — refresh broadly.
      qc.invalidateQueries({ queryKey: queryKeys.activity })
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
      qc.invalidateQueries({ queryKey: queryKeys.instance.baseDomain })
    },
  })
}
