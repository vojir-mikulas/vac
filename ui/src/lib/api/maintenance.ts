import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// Maintenance mode + editable page (docs/plans/maintenance-mode-and-deploy-gates.md).
// The operator can put an app into maintenance so Caddy serves a 503 page, opt
// into showing it automatically during deploys, and override the page HTML.

export interface MaintenanceState {
  /** Operator-set manual maintenance (sticky — survives deploys). */
  enabled: boolean
  /** Show the maintenance page automatically during a deploy. */
  auto: boolean
  /** Effective runtime flag — the page is being served right now. */
  active: boolean
  /** A custom page is stored (vs the built-in default). */
  has_custom_page: boolean
}

export interface MaintenancePage {
  /** The effective page HTML (custom when set, otherwise the built-in default). */
  html: string
  /** True when no custom page is stored. */
  is_default: boolean
}

export const maintenanceApi = {
  get: (appId: string) => api.get<MaintenanceState>(`apps/${appId}/maintenance`),
  set: (appId: string, enabled: boolean, auto: boolean) =>
    api.put<MaintenanceState>(`apps/${appId}/maintenance`, { enabled, auto }),
  getPage: (appId: string) => api.get<MaintenancePage>(`apps/${appId}/maintenance/page`),
  savePage: (appId: string, html: string) =>
    api.put<MaintenancePage>(`apps/${appId}/maintenance/page`, { html }),
  resetPage: (appId: string) => api.del<MaintenancePage>(`apps/${appId}/maintenance/page`),
}

export function useMaintenance(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.maintenance(appId),
    queryFn: () => maintenanceApi.get(appId),
  })
}

export function useSetMaintenance(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: { enabled: boolean; auto: boolean }) =>
      maintenanceApi.set(appId, vars.enabled, vars.auto),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.maintenance(appId) })
      // The overview badge reads maintenance_active off the app DTO.
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(appId) })
    },
  })
}

export function useMaintenancePage(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.maintenancePage(appId),
    queryFn: () => maintenanceApi.getPage(appId),
  })
}

export function useSaveMaintenancePage(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (html: string) => maintenanceApi.savePage(appId, html),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.maintenancePage(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.maintenance(appId) })
    },
  })
}

export function useResetMaintenancePage(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => maintenanceApi.resetPage(appId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.maintenancePage(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.maintenance(appId) })
    },
  })
}
