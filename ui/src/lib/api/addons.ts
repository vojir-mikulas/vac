import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Addon, AddonInstallResult } from '@/types/api'

export const addonsApi = {
  list: () => api.get<Addon[]>('addons'),
  get: (id: string) => api.get<Addon>(`addons/${id}`),
  install: (id: string, name?: string) =>
    api.post<AddonInstallResult>(`addons/${id}/install`, { name }),
}

export function useAddons(enabled = true) {
  return useQuery({
    queryKey: queryKeys.addons.all,
    queryFn: () => addonsApi.list(),
    enabled,
    staleTime: 5 * 60_000,
  })
}

export function useInstallAddon() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name?: string }) => addonsApi.install(id, name),
    onSuccess: () => {
      // A new install is a new app.
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
    },
  })
}
