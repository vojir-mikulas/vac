import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Domain } from '@/types/api'

export const domainsApi = {
  list: (appId: string) => api.get<Domain[]>(`apps/${appId}/domains`),
  create: (appId: string, service: string, hostname: string) =>
    api.post<Domain>(`apps/${appId}/services/${service}/domains`, { hostname }),
  remove: (appId: string, domainId: string) =>
    api.del<{ status: string }>(`apps/${appId}/domains/${domainId}`),
}

export function useDomains(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.domains(appId),
    queryFn: () => domainsApi.list(appId),
  })
}

export function useCreateDomain(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ service, hostname }: { service: string; hostname: string }) =>
      domainsApi.create(appId, service, hostname),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.domains(appId) }),
  })
}

export function useDeleteDomain(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (domainId: string) => domainsApi.remove(appId, domainId),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.domains(appId) }),
  })
}
