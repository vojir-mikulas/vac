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
  has_preview: boolean // carries a before-snapshot — previewable even once reverted
  reverted_at?: string
  created_at: string
}

// A before→current diff for a curated entry (plan 22). Values are server-side
// sanitized: sensitive/write-only env values are masked, never sent.
export type DiffStatus = 'added' | 'removed' | 'changed' | 'unchanged'
export interface DiffRow {
  label: string
  status: DiffStatus
  before?: string
  after?: string
  masked: boolean
}
export interface ActivityDiff {
  kind: 'env' | 'app' | 'base_domain'
  rows: DiffRow[]
  current_as_of: string
  changed_since: boolean
}

export const auditApi = {
  list: (limit = 100) => api.get<AuditEntry[]>(`audit?limit=${limit}`),
  revert: (id: string) => api.post<{ reverted: string; summary: string }>(`audit/${id}/revert`),
  diff: (id: string) => api.get<ActivityDiff>(`audit/${id}/diff`),
}

export function useActivity(limit = 100) {
  return useQuery({
    queryKey: queryKeys.activity,
    queryFn: () => auditApi.list(limit),
    refetchInterval: 30_000,
  })
}

// useActivityDiff lazily fetches the before→current diff for one entry — only
// once `id` is non-null (i.e. a preview dialog is open). Always re-fetches fresh
// since the "after" side is current DB state.
export function useActivityDiff(id: string | null) {
  return useQuery({
    queryKey: [...queryKeys.activity, 'diff', id],
    queryFn: () => auditApi.diff(id!),
    enabled: !!id,
    staleTime: 0,
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
