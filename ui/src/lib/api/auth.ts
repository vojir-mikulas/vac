import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type {
  ApiToken,
  CreatedApiToken,
  LoginInput,
  LoginResult,
  Session,
  TotpSetup,
  User,
} from '@/types/api'

export const authApi = {
  me: () => api.get<User>('auth/me'),
  login: (input: LoginInput) => api.post<LoginResult>('auth/login', input),
  totpLogin: (body: { code: string } | { recovery_code: string }) =>
    api.post<User>('auth/totp', body),
  logout: () => api.post<{ status: string }>('auth/logout'),

  totpSetup: () => api.post<TotpSetup>('auth/totp/setup'),
  totpEnable: (code: string) =>
    api.post<{ recovery_codes: string[] }>('auth/totp/enable', { code }),
  totpDisable: (password: string) => api.del<{ status: string }>('auth/totp', { password }),

  // Re-prove identity on the live session to unlock destructive actions for a
  // short window: a 2FA code/recovery code when TOTP is enabled, or a password
  // re-entry when it isn't. Used by the global step-up prompt, not the login flow.
  stepUp: (body: { code: string } | { recovery_code: string } | { password: string }) =>
    api.post<{ status: string }>('auth/step-up', body),

  sessions: () => api.get<Session[]>('auth/sessions'),
  revokeSession: (id: string) => api.del<{ revoked: number }>(`auth/sessions/${id}`),
  revokeOtherSessions: () => api.del<{ revoked: number }>('auth/sessions'),

  apiTokens: () => api.get<ApiToken[]>('auth/api-tokens'),
  createApiToken: (name: string, expiresInDays: number) =>
    api.post<CreatedApiToken>('auth/api-tokens', {
      name,
      expires_in_days: expiresInDays,
    }),
  revokeApiToken: (id: string) => api.del<{ revoked: number }>(`auth/api-tokens/${id}`),
}

export function useMe() {
  return useQuery({
    queryKey: queryKeys.auth.me,
    queryFn: () => authApi.me(),
    staleTime: 5 * 60_000,
    retry: false,
  })
}

export function useSessions() {
  return useQuery({
    queryKey: queryKeys.auth.sessions,
    queryFn: () => authApi.sessions(),
  })
}

export function useApiTokens() {
  return useQuery({
    queryKey: queryKeys.auth.apiTokens,
    queryFn: () => authApi.apiTokens(),
  })
}

export function useLogout() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: () => authApi.logout(),
    onSuccess: async () => {
      qc.clear()
      await navigate({ to: '/login' })
    },
  })
}
