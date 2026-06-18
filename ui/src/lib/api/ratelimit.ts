import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// Per-app edge rate limit. The operator caps requests-per-minute-per-IP; Caddy
// enforces it (caddy-ratelimit). null = no limit.
export interface RateLimitState {
  rpm: number | null
}

export const rateLimitApi = {
  get: (appId: string) => api.get<RateLimitState>(`apps/${appId}/rate-limit`),
  set: (appId: string, rpm: number | null) =>
    api.put<RateLimitState>(`apps/${appId}/rate-limit`, { rpm }),
}

export function useRateLimit(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.rateLimit(appId),
    queryFn: () => rateLimitApi.get(appId),
  })
}

export function useSetRateLimit(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (rpm: number | null) => rateLimitApi.set(appId, rpm),
    onSuccess: (state) => {
      qc.setQueryData(queryKeys.apps.rateLimit(appId), state)
    },
  })
}
