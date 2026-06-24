import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { ApiError, api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type {
  App,
  AppVolumes,
  BuildDetectResult,
  ComposeDetectResult,
  CreateAppInput,
  EnvExampleResult,
  RegistryAuthConfig,
  RegistryAuthInput,
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
  detectBuild: (input: { git_url: string; git_branch: string }) =>
    api.post<BuildDetectResult>('apps/detect-build', input),
  sshKey: (id: string) => api.get<SSHKey>(`apps/${id}/ssh-key`),
  regenerateSshKey: (id: string) => api.post<SSHKey>(`apps/${id}/ssh-key/regenerate`),
  volumes: (id: string) => api.get<AppVolumes>(`apps/${id}/volumes`),
  registryAuth: (id: string) => api.get<RegistryAuthConfig>(`apps/${id}/registry-auth`),
  setRegistryAuth: (id: string, input: RegistryAuthInput) =>
    api.put<RegistryAuthConfig>(`apps/${id}/registry-auth`, input),
  clearRegistryAuth: (id: string) => api.del<RegistryAuthConfig>(`apps/${id}/registry-auth`),
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
    // A 404 is definitive (app deleted / never existed) — don't retry it, so the
    // not-found state surfaces immediately instead of after the default backoff.
    retry: (count, err) => !(err instanceof ApiError && err.status === 404) && count < 1,
  })
}

// Volume usage is a slow-changing periodic snapshot (the collector samples every
// few minutes), so the default cache behaviour is fine — no live subscription.
export function useAppVolumes(id: string) {
  return useQuery({
    queryKey: queryKeys.apps.volumes(id),
    queryFn: () => appsApi.volumes(id),
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
    onSuccess: (_data, id) => {
      // Drop the deleted app's detail cache so navigating back to its URL shows
      // the not-found state rather than lingering stale data.
      qc.removeQueries({ queryKey: queryKeys.apps.detail(id) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
    },
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
