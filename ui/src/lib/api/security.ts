import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { Fail2banState, FirewallState, PostureFinding, TrafficSnapshot } from '@/types/api'

export const securityApi = {
  posture: () => api.get<PostureFinding[]>('security/posture'),
  traffic: () => api.get<TrafficSnapshot>('security/traffic'),
  fail2ban: () => api.get<Fail2banState>('security/fail2ban'),
  firewall: () => api.get<FirewallState>('security/firewall'),
}

export function useSecurityPosture() {
  return useQuery({
    queryKey: queryKeys.security.posture,
    queryFn: () => securityApi.posture(),
  })
}

export function useSecurityTraffic() {
  return useQuery({
    queryKey: queryKeys.security.traffic,
    queryFn: () => securityApi.traffic(),
    // Short interval for a live feel without a dedicated WebSocket.
    refetchInterval: 5_000,
  })
}

export function useFail2ban() {
  return useQuery({
    queryKey: queryKeys.security.fail2ban,
    queryFn: () => securityApi.fail2ban(),
  })
}

export function useFirewall() {
  return useQuery({
    queryKey: queryKeys.security.firewall,
    queryFn: () => securityApi.firewall(),
  })
}
