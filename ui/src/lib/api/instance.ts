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
  /** P3.4 gate — hides the interactive container-shell affordance when off. */
  enable_shell: boolean
}

export interface UpdateInfo {
  current: string
  latest: string
  update_available: boolean
  release_url: string
  checked_at: string
  /** Non-empty when the upstream check failed; the card shows "couldn't check". */
  error?: string
}

export interface DiskUsageEntry {
  type: string
  total_count: number
  active: number
  size_bytes: number
  reclaimable_bytes: number
}

export interface DiskUsage {
  images: DiskUsageEntry
  containers: DiskUsageEntry
  volumes: DiskUsageEntry
  build_cache: DiskUsageEntry
}

export interface PruneResult {
  images_reclaimed_bytes: number
  build_cache_reclaimed_bytes: number
  total_reclaimed_bytes: number
}

export interface BaseDomainInfo {
  base_domain: string // the runtime override ("" = unset, using config)
  effective: string // override or config fallback
  /** Where `effective` comes from, so the card can label its origin. */
  source: 'override' | 'env' | 'file' | 'unset'
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

export interface DeployConcurrency {
  max_concurrent_deploys: number
  min: number
  max: number
}

export const instanceApi = {
  info: () => api.get<InstanceInfo>('instance/info'),
  updateCheck: () => api.get<UpdateInfo>('instance/update-check'),
  diskUsage: () => api.get<DiskUsage>('instance/disk'),
  pruneDisk: () => api.post<PruneResult>('instance/prune'),
  getBaseDomain: () => api.get<BaseDomainInfo>('instance/base-domain'),
  setBaseDomain: (baseDomain: string) =>
    api.put<BaseDomainInfo>('instance/base-domain', { base_domain: baseDomain }),
  getDeployConcurrency: () => api.get<DeployConcurrency>('instance/deploy-concurrency'),
  setDeployConcurrency: (n: number) =>
    api.put<DeployConcurrency>('instance/deploy-concurrency', { max_concurrent_deploys: n }),
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

export function useUpdateCheck() {
  return useQuery({
    queryKey: queryKeys.instance.updateCheck,
    queryFn: () => instanceApi.updateCheck(),
    // Cached server-side for an hour; a 30-min client window keeps the badge
    // fresh without re-fetching on every settings visit.
    staleTime: 30 * 60_000,
  })
}

export function useDiskUsage() {
  return useQuery({
    queryKey: queryKeys.instance.disk,
    queryFn: () => instanceApi.diskUsage(),
    staleTime: 60_000,
  })
}

export function usePruneDisk() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => instanceApi.pruneDisk(),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.instance.disk }),
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

/**
 * Auto-probe a host's DNS so the UI can show wildcard status without the
 * operator clicking "Check". Cached for a few minutes so revisiting Settings
 * doesn't re-probe on every mount; `enabled` lets callers hold off until a
 * base domain exists. Manual rechecks invalidate this key.
 */
export function useDnsCheck(host: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.instance.dnsCheck(host),
    queryFn: () => instanceApi.dnsCheck(host),
    enabled: enabled && host.length > 0,
    staleTime: 5 * 60_000,
  })
}

export function useDeployConcurrency() {
  return useQuery({
    queryKey: queryKeys.instance.deployConcurrency,
    queryFn: () => instanceApi.getDeployConcurrency(),
  })
}

export function useSetDeployConcurrency() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (n: number) => instanceApi.setDeployConcurrency(n),
    onSuccess: (v) => qc.setQueryData(queryKeys.instance.deployConcurrency, v),
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
