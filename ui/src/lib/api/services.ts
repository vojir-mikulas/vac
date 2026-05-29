import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Service, UpdateServiceInput } from '@/types/api'

export const servicesApi = {
  list: (appId: string) => api.get<Service[]>(`apps/${appId}/services`),
  update: (appId: string, name: string, input: UpdateServiceInput) =>
    api.patch<Service>(`apps/${appId}/services/${name}`, input),
  restart: (appId: string, name: string) =>
    api.post<{ status: string }>(`apps/${appId}/services/${name}/restart`),
}

export function useServices(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.services(appId),
    queryFn: () => servicesApi.list(appId),
  })
}

export function useUpdateService(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, input }: { name: string; input: UpdateServiceInput }) =>
      servicesApi.update(appId, name, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.services(appId) }),
  })
}

export function useRestartService(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => servicesApi.restart(appId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.services(appId) }),
  })
}
