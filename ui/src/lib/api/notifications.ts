import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { NotificationSettings, UpdateNotificationInput } from '@/types/api'

export const notificationsApi = {
  get: () => api.get<NotificationSettings>('settings/notifications'),
  update: (input: UpdateNotificationInput) =>
    api.put<{ status: string }>('settings/notifications', input),
  test: () => api.post<{ sent: number }>('settings/notifications/test'),
}

export function useNotificationSettings() {
  return useQuery({
    queryKey: queryKeys.notifications,
    queryFn: () => notificationsApi.get(),
  })
}

export function useUpdateNotifications() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: UpdateNotificationInput) => notificationsApi.update(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.notifications }),
  })
}
