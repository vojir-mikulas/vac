import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type {
  App,
  ComposeDetectResult,
  CreateAppInput,
  EnvExampleResult,
  SSHKey,
  TestConnectionResult,
  UpdateAppInput,
} from '@/types/api'

export const appsApi = {
  list: () => api.get<App[]>('apps'),
  get: (id: string) => api.get<App>(`apps/${id}`),
  create: (input: CreateAppInput) => api.post<App>('apps', input),
  update: (id: string, input: UpdateAppInput) => api.patch<App>(`apps/${id}`, input),
  remove: (id: string) => api.del<{ deleted: number }>(`apps/${id}`),
  start: (id: string) => api.post<{ status: string }>(`apps/${id}/start`),
  stop: (id: string) => api.post<{ status: string }>(`apps/${id}/stop`),
  restart: (id: string) => api.post<{ status: string }>(`apps/${id}/restart`),
  testConnection: (id: string) => api.post<TestConnectionResult>(`apps/${id}/test-connection`),
  envExample: (input: { git_url: string; git_branch: string }) =>
    api.post<EnvExampleResult>('apps/env-example', input),
  detectCompose: (input: { git_url: string; git_branch: string }) =>
    api.post<ComposeDetectResult>('apps/detect-compose', input),
  sshKey: (id: string) => api.get<SSHKey>(`apps/${id}/ssh-key`),
  regenerateSshKey: (id: string) => api.post<SSHKey>(`apps/${id}/ssh-key/regenerate`),
}

export function useApps() {
  return useQuery({
    queryKey: queryKeys.apps.all,
    queryFn: () => appsApi.list(),
  })
}

export function useApp(id: string) {
  return useQuery({
    queryKey: queryKeys.apps.detail(id),
    queryFn: () => appsApi.get(id),
  })
}

export function useCreateApp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateAppInput) => appsApi.create(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.all }),
  })
}

export function useUpdateApp(id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: UpdateAppInput) => appsApi.update(id, input),
    onSuccess: (app) => {
      qc.setQueryData(queryKeys.apps.detail(id), app)
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
    },
  })
}

export function useDeleteApp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => appsApi.remove(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.all }),
  })
}

type StackAction = 'start' | 'stop' | 'restart'

export function useStackControl(id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (action: StackAction) => appsApi[action](id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(id) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.services(id) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
    },
  })
}
