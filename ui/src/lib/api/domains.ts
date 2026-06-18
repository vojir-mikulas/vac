import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { CertMeta, Domain, DomainStatusState } from '@/types/api'

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

  // Bring-your-own TLS cert (plan B). Both are step-up gated server-side; the
  // global client handles the 403 step_up_required prompt + retry transparently.
  uploadCert: (id: string, certPem: string, keyPem: string) =>
    api.post<CertMeta>(`domains/${id}/cert`, { cert_pem: certPem, key_pem: keyPem }),
  clearCert: (id: string) => api.del<{ status: string }>(`domains/${id}/cert`),
}

// Per-app domains (custom + derived auto hosts). Polls while any domain is still
// settling (cert issuing, DNS check) so the app-detail view advances its status
// live instead of freezing at page-load — mirrors useAllDomains.
export function useDomains(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.domains(appId),
    queryFn: () => domainsApi.list(appId),
    refetchInterval: (query) => {
      const rows = query.state.data
      if (!rows) return false
      const settling = rows.some((d) => d.status && d.status !== 'active' && d.status !== 'error')
      return settling ? 10_000 : false
    },
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

// invalidateDomainViews refreshes every domain list (the hub plus each app's
// view) after a cert change, so an upload/clear is reflected wherever it shows.
function invalidateDomainViews(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: queryKeys.domains })
  void qc.invalidateQueries({
    predicate: (q) => q.queryKey[0] === 'apps' && q.queryKey[2] === 'domains',
  })
}

/** Upload a bring-your-own TLS cert for a domain (plan B). */
export function useUploadDomainCert() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, certPem, keyPem }: { id: string; certPem: string; keyPem: string }) =>
      domainsApi.uploadCert(id, certPem, keyPem),
    onSuccess: () => invalidateDomainViews(qc),
  })
}

/** Remove an uploaded cert and revert the domain to ACME (plan B). */
export function useClearDomainCert() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => domainsApi.clearCert(id),
    onSuccess: () => invalidateDomainViews(qc),
  })
}
