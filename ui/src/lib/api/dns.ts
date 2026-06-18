import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { DNSSettings } from '@/types/api'

/** Body for saving DNS-provider settings. An empty token keeps the stored one. */
export interface UpdateDNSSettingsInput {
  provider: string // '' clears the configuration
  zone: string
  token?: string
}

export const dnsApi = {
  get: () => api.get<DNSSettings>('settings/dns'),
  // Step-up gated server-side; the global client handles the 403 prompt + retry.
  update: (input: UpdateDNSSettingsInput) => api.put<DNSSettings>('settings/dns', input),
}

export function useDNSSettings() {
  return useQuery({
    queryKey: queryKeys.dnsSettings,
    queryFn: () => dnsApi.get(),
  })
}

export function useUpdateDNSSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: UpdateDNSSettingsInput) => dnsApi.update(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.dnsSettings }),
  })
}
