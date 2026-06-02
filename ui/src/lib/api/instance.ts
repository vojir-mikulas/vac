import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

export interface InstanceInfo {
  version: string
  commit: string
  built_at: string
  channel: string
  /** Track D master gate — hides backups/databases/add-ons surfaces when off. */
  managed_services: boolean
}

export interface BaseDomainInfo {
  base_domain: string // the runtime override ("" = unset, using config)
  effective: string // override or config fallback
}

export interface DnsCheckResult {
  host: string
  ip: string
  resolved: string[]
  points_here: boolean
  error?: string
}

export interface OnboardingState {
  dismissed: boolean
}

export const instanceApi = {
  info: () => api.get<InstanceInfo>('instance/info'),
  getBaseDomain: () => api.get<BaseDomainInfo>('instance/base-domain'),
  setBaseDomain: (baseDomain: string) =>
    api.put<BaseDomainInfo>('instance/base-domain', { base_domain: baseDomain }),
  dnsCheck: (host: string) =>
    api.get<DnsCheckResult>(`instance/dns-check?host=${encodeURIComponent(host)}`),
  restartControlPlane: () => api.post<{ status: string }>('instance/restart-control-plane'),
  stopAllApps: () => api.post<{ stopped: number; failed: number }>('instance/stop-all-apps'),
  reset: (confirm: string) =>
    api.post<{ removed: number; failed: number }>('instance/reset', { confirm }),
  onboarding: () => api.get<OnboardingState>('instance/onboarding'),
  dismissOnboarding: () => api.post<OnboardingState>('instance/onboarding/dismiss'),
}

export function useInstanceInfo() {
  return useQuery({
    queryKey: queryKeys.instance.info,
    queryFn: () => instanceApi.info(),
    staleTime: 5 * 60_000,
  })
}

export function useBaseDomain() {
  return useQuery({
    queryKey: queryKeys.instance.baseDomain,
    queryFn: () => instanceApi.getBaseDomain(),
  })
}

export function useSetBaseDomain() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (baseDomain: string) => instanceApi.setBaseDomain(baseDomain),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.instance.baseDomain }),
  })
}

export function useOnboarding() {
  return useQuery({
    queryKey: queryKeys.instance.onboarding,
    queryFn: () => instanceApi.onboarding(),
    staleTime: 5 * 60_000,
  })
}

export function useDismissOnboarding() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => instanceApi.dismissOnboarding(),
    onSuccess: (state) => qc.setQueryData(queryKeys.instance.onboarding, state),
  })
}
