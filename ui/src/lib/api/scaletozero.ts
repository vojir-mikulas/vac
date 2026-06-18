import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// Scale-to-zero per-app config (docs/plans/scale-to-zero.md). The operator opts
// an app into idle-suspend and optionally overrides the inactivity window; the
// sweeper (gated by the instance idle_suspend flag) stops it when idle and a
// request wakes it back up.

export interface IdleSuspendState {
  /** Per-app opt-in. */
  enabled: boolean
  /** Per-app idle window override in minutes; null = the instance default. */
  timeout_minutes: number | null
  /** True when the stack is currently stopped and serving a wake route. */
  suspended: boolean
  /** Last observed inbound-request time; absent if none seen yet. */
  last_traffic_at?: string
}

export const idleSuspendApi = {
  get: (appId: string) => api.get<IdleSuspendState>(`apps/${appId}/idle-suspend`),
  set: (appId: string, enabled: boolean, timeoutMinutes: number | null) =>
    api.put<IdleSuspendState>(`apps/${appId}/idle-suspend`, {
      enabled,
      timeout_minutes: timeoutMinutes,
    }),
}

export function useIdleSuspend(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.idleSuspend(appId),
    queryFn: () => idleSuspendApi.get(appId),
  })
}

export function useSetIdleSuspend(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: { enabled: boolean; timeout_minutes: number | null }) =>
      idleSuspendApi.set(appId, vars.enabled, vars.timeout_minutes),
    onSuccess: (state) => {
      qc.setQueryData(queryKeys.apps.idleSuspend(appId), state)
      // The overview badge reads `suspended` off the app DTO.
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
    },
  })
}
