import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type {
  Fail2banState,
  FirewallState,
  PostureFinding,
  SecurityAttempt,
  TrafficSnapshot,
} from '@/types/api'

export const securityApi = {
  posture: () => api.get<PostureFinding[]>('security/posture'),
  traffic: () => api.get<TrafficSnapshot>('security/traffic'),
  attempts: (limit = 200) => api.get<SecurityAttempt[]>(`security/attempts?limit=${limit}`),
  fail2ban: () => api.get<Fail2banState>('security/fail2ban'),
  firewall: () => api.get<FirewallState>('security/firewall'),
}

// useUnauthorizedAttempts reads the diverted unauthenticated attempts (failed
// logins, probes to bogus endpoints). Surfaced on the Activity page — unlike the
// fail2ban/firewall panels it needs no host agent, so it's always available.
export function useUnauthorizedAttempts(limit = 200) {
  return useQuery({
    queryKey: queryKeys.security.attempts,
    queryFn: () => securityApi.attempts(limit),
    refetchInterval: 30_000,
  })
}

export function useSecurityPosture() {
  return useQuery({
    queryKey: queryKeys.security.posture,
    queryFn: () => securityApi.posture(),
    // Re-poll so the posture summary lights up shortly after host state changes
    // (e.g. the firewall comes up, or the host agent's snapshot refreshes).
    refetchInterval: 15_000,
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
    refetchInterval: 15_000,
  })
}

export function useFirewall() {
  return useQuery({
    queryKey: queryKeys.security.firewall,
    queryFn: () => securityApi.firewall(),
    refetchInterval: 15_000,
  })
}

// useSecurityAttention collapses the posture checklist into a single badge
// signal for the sidebar: how many findings need attention and the worst
// severity among them. Mirrors the page's PostureSummary so the badge count
// matches its "N issues need attention" headline. Reuses the posture query —
// no extra request beyond what the page already polls.
export function useSecurityAttention(): {
  count: number
  severity: 'warn' | 'error' | null
} {
  const { data } = useSecurityPosture()
  const findings = data ?? []
  const errors = findings.filter((f) => f.severity === 'error').length
  const warns = findings.filter((f) => f.severity === 'warn').length
  return {
    count: errors + warns,
    severity: errors > 0 ? 'error' : warns > 0 ? 'warn' : null,
  }
}
