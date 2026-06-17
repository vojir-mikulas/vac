import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// Push-to-deploy (plan 01): per-app trigger rules + the inbound webhook secret.

export type TriggerEvent = 'push' | 'tag' | 'preview'

export interface DeployTrigger {
  id: string
  event: TriggerEvent
  filter: string
  created_at: string
}

export interface WebhookConfig {
  url: string
  configured: boolean
}

export interface WebhookSecret {
  url: string
  secret: string
}

export const autoDeployApi = {
  listTriggers: (appId: string) => api.get<DeployTrigger[]>(`apps/${appId}/triggers`),
  createTrigger: (appId: string, event: TriggerEvent, filter: string) =>
    api.post<DeployTrigger>(`apps/${appId}/triggers`, { event, filter }),
  deleteTrigger: (appId: string, triggerId: string) =>
    api.del<void>(`apps/${appId}/triggers/${triggerId}`),
  getWebhook: (appId: string) => api.get<WebhookConfig>(`apps/${appId}/webhook`),
  regenerateWebhook: (appId: string) => api.post<WebhookSecret>(`apps/${appId}/webhook/regenerate`),
  disableWebhook: (appId: string) => api.del<void>(`apps/${appId}/webhook`),
}

export function useTriggers(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.triggers(appId),
    queryFn: () => autoDeployApi.listTriggers(appId),
  })
}

export function useCreateTrigger(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: { event: TriggerEvent; filter: string }) =>
      autoDeployApi.createTrigger(appId, vars.event, vars.filter),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.triggers(appId) }),
  })
}

export function useDeleteTrigger(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (triggerId: string) => autoDeployApi.deleteTrigger(appId, triggerId),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.triggers(appId) }),
  })
}

export function useWebhookConfig(appId: string) {
  return useQuery({
    queryKey: queryKeys.apps.webhook(appId),
    queryFn: () => autoDeployApi.getWebhook(appId),
  })
}

export function useRegenerateWebhook(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => autoDeployApi.regenerateWebhook(appId),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.webhook(appId) }),
  })
}

export function useDisableWebhook(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => autoDeployApi.disableWebhook(appId),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.webhook(appId) }),
  })
}
