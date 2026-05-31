import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { SetupStatus, User } from '@/types/api'

export const setupApi = {
  status: () => api.get<SetupStatus>('setup/status'),
  createAdmin: (username: string, password: string) =>
    api.post<User>('setup/admin', {
      username,
      password,
    }),
}

export function useSetupStatus() {
  return useQuery({
    queryKey: queryKeys.setup.status,
    queryFn: () => setupApi.status(),
    staleTime: Infinity,
    retry: false,
  })
}
