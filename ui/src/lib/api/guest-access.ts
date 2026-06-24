import { api } from '@/lib/api/client'

// Per-service shared access code for the VAC login gate (internal/guard). When
// set, a non-operator who enters the code can reach this one guarded service —
// without any VAC dashboard access. The code is sealed at rest; `enabled`
// reports whether one is set (the service DTO also carries guest_access_enabled).
export interface GuestAccessState {
  enabled: boolean
}

export const guestAccessApi = {
  get: (appId: string, name: string) =>
    api.get<GuestAccessState>(`apps/${appId}/services/${name}/guest-access`),
  set: (appId: string, name: string, code: string) =>
    api.put<GuestAccessState>(`apps/${appId}/services/${name}/guest-access`, { code }),
  remove: (appId: string, name: string) =>
    api.del<{ cleared: number }>(`apps/${appId}/services/${name}/guest-access`),
  reveal: (appId: string, name: string) =>
    api.get<{ code: string }>(`apps/${appId}/services/${name}/guest-access/reveal`),
}
