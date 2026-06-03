import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { EnvVar } from '@/types/api'

// Write shape for the full-replace PUT. `value` is sent unless `keep` is set;
// `sensitive` governs only how the value is read back. `write_only` marks the
// key unrevealable (implies sensitive); `keep` reuses the existing sealed value
// instead of re-sealing `value` — the only way an untouched write-only secret
// survives a full-replace, since its plaintext can never be fetched.
export interface EnvVarInput {
  key: string
  value: string
  sensitive: boolean
  write_only: boolean
  keep?: boolean
}

export const envApi = {
  list: (appId: string) => api.get<EnvVar[]>(`apps/${appId}/env`),
  replace: (appId: string, vars: EnvVarInput[]) =>
    api.put<{ saved: number }>(`apps/${appId}/env`, { vars }),
  reveal: (appId: string, key: string) =>
    api.get<EnvVar>(`apps/${appId}/env/${encodeURIComponent(key)}/reveal`),
}

export function useEnvVars(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.env(appId),
    queryFn: () => envApi.list(appId),
  })
}

export function useReplaceEnv(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: EnvVarInput[]) => envApi.replace(appId, vars),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.env(appId) }),
  })
}
