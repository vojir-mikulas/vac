import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Domain, DomainStatusState } from '@/types/api'

export interface DomainStatus {
  state: DomainStatusState
  detail?: string
  cert_not_after?: string
  last_checked?: string
}

/** Optional service binding for a custom domain (both set or both blank). */
export interface DomainAssignment {
  app_id?: string
  service_name?: string
}

/** Editable fields for a custom domain (plan 09 Phase 2 & 3). */
export type UpdateDomainBody = { hostname?: string; redirect_to?: string } & DomainAssignment

export const domainsApi = {
  // Per-app view (custom + derived auto hosts).
  list: (appId: string) => api.get<Domain[]>(`apps/${appId}/domains`),
  create: (appId: string, service: string, hostname: string) =>
    api.post<Domain>(`apps/${appId}/services/${service}/domains`, { hostname }),
  remove: (appId: string, domainId: string) =>
    api.del<{ status: string }>(`apps/${appId}/domains/${domainId}`),

  // Domains hub (manage everything in one place).
  listAll: () => api.get<Domain[]>('domains'),
  add: (hostname: string, assign?: DomainAssignment) =>
    api.post<Domain>('domains', { hostname, ...assign }),
  update: (id: string, body: UpdateDomainBody) => api.patch<Domain>(`domains/${id}`, body),
  removeById: (id: string) => api.del<{ status: string }>(`domains/${id}`),
  refresh: (hostname: string) =>
    api.post<DomainStatus>(`domains/refresh?host=${encodeURIComponent(hostname)}`),
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

// ---- Domains hub ----

/**
 * The full domain list across all apps, including derived auto hosts. Polls
 * while any visible domain has not reached `active` (plan 09 F3 — status updates
 * itself), then idles.
 */
export function useAllDomains() {
  return useQuery({
    queryKey: queryKeys.domains,
    queryFn: () => domainsApi.listAll(),
    refetchInterval: (query) => {
      const rows = query.state.data
      if (!rows) return false
      const settling = rows.some((d) => d.status && d.status !== 'active' && d.status !== 'error')
      return settling ? 10_000 : false
    },
  })
}

export function useAddDomain() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ hostname, assign }: { hostname: string; assign?: DomainAssignment }) =>
      domainsApi.add(hostname, assign),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.domains }),
  })
}

export function useUpdateDomain() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateDomainBody }) =>
      domainsApi.update(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.domains }),
  })
}

export function useDeleteDomainById() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => domainsApi.removeById(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.domains }),
  })
}

export function useRefreshDomainStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (hostname: string) => domainsApi.refresh(hostname),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.domains }),
  })
}
