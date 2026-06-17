import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import { isDeployActive } from '@/lib/deploy-status'
import type { Preview, PreviewBudget } from '@/types/api'

export const previewsApi = {
  list: (appId: string) => api.get<Preview[]>(`apps/${appId}/previews`),
  // Step-up gated server-side (down -v removes volumes); the api client handles
  // the 2FA challenge + retry transparently.
  teardown: (appId: string, previewId: string) =>
    api.del<{ deleted: number }>(`apps/${appId}/previews/${previewId}`),
  budget: () => api.get<PreviewBudget>('apps/previews/budget'),
}

export function usePreviews(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.previews(appId),
    queryFn: () => previewsApi.list(appId),
    // Poll while a preview is mid-deploy so its status pill advances live.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((p) => isDeployActive(p.status)) ? 3_000 : false,
  })
}

export function usePreviewBudget() {
  return useQuery({
    queryKey: queryKeys.apps.previewBudget,
    queryFn: () => previewsApi.budget(),
  })
}

export function useTeardownPreview(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (previewId: string) => previewsApi.teardown(appId, previewId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.previews(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.previewBudget })
      // A preview is itself an app row, so the apps list changes too.
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
    },
  })
}
