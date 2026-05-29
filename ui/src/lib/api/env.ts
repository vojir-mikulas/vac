import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { EnvVarKey } from '@/types/api'

export const envApi = {
  list: (appId: string) => api.get<EnvVarKey[]>(`apps/${appId}/env`),
  replace: (appId: string, vars: Record<string, string>) =>
    api.put<{ saved: number }>(`apps/${appId}/env`, { vars }),
}

export function useEnvKeys(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.env(appId),
    queryFn: () => envApi.list(appId),
  })
}

export function useReplaceEnv(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: Record<string, string>) => envApi.replace(appId, vars),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.env(appId) }),
  })
}
