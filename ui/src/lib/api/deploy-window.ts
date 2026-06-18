import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// Deploy windows (docs/plans/maintenance-mode-and-deploy-gates.md, Phase 3).
// Restrict push-to-deploy to one or more time windows; a push outside every
// window is parked as a `scheduled` deploy and released when a window opens.

export interface DeployWindow {
  /** Weekday numbers 0=Sun…6=Sat; empty = every day. */
  days: number[]
  /** "HH:MM" 24-hour start, local to tz. */
  start: string
  /** "HH:MM" 24-hour end; before start = wraps past midnight. */
  end: string
  /** IANA timezone name; empty = UTC. */
  tz: string
}

export interface DeployWindowConfig {
  windows: DeployWindow[]
}

export const deployWindowApi = {
  get: (appId: string) => api.get<DeployWindowConfig>(`apps/${appId}/deploy-window`),
  save: (appId: string, windows: DeployWindow[]) =>
    api.put<DeployWindowConfig>(`apps/${appId}/deploy-window`, { windows }),
}

export function useDeployWindow(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.deployWindow(appId),
    queryFn: () => deployWindowApi.get(appId),
  })
}

export function useSaveDeployWindow(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (windows: DeployWindow[]) => deployWindowApi.save(appId, windows),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.deployWindow(appId) }),
  })
}
