import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { AddDatabaseResult, DBEngineInfo, ManagedDatabase } from '@/types/api'

export const databasesApi = {
  list: (appId: string) => api.get<ManagedDatabase[]>(`apps/${appId}/databases`),
  engines: (appId: string) => api.get<DBEngineInfo[]>(`apps/${appId}/databases/engines`),
  add: (appId: string, engine: string, envVarName?: string) =>
    api.post<AddDatabaseResult>(`apps/${appId}/databases`, {
      engine,
      env_var_name: envVarName || undefined,
    }),
  remove: (appId: string, dbid: string) => api.del<void>(`apps/${appId}/databases/${dbid}`),
}

export function useDatabases(appId: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.databases(appId),
    queryFn: () => databasesApi.list(appId),
    enabled,
    // Poll while any DB is still provisioning so the status pill settles on its own.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((d) => d.status === 'provisioning') ? 3_000 : false,
  })
}

export function useDatabaseEngines(appId: string, enabled = true) {
  return useQuery({
    queryKey: [...queryKeys.apps.databases(appId), 'engines'],
    queryFn: () => databasesApi.engines(appId),
    enabled,
    staleTime: 5 * 60_000,
  })
}

export function useAddDatabase(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ engine, envVarName }: { engine: string; envVarName?: string }) =>
      databasesApi.add(appId, engine, envVarName),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.databases(appId) })
      // A new managed DB injects an env var, so the env list changes too.
      qc.invalidateQueries({ queryKey: queryKeys.apps.env(appId) })
    },
  })
}

// useAddDatabaseToApp is the add-on-catalog variant of useAddDatabase: the app
// is chosen at mutate time (the catalog isn't scoped to one app), then it hits
// the same per-app provisioning endpoint.
export function useAddDatabaseToApp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      appId,
      engine,
      envVarName,
    }: {
      appId: string
      engine: string
      envVarName?: string
    }) => databasesApi.add(appId, engine, envVarName),
    onSuccess: (_res, { appId }) => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.databases(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.env(appId) })
      // The box-wide inventory powers the catalog's live "Active" state.
      qc.invalidateQueries({ queryKey: queryKeys.databases.inventory })
    },
  })
}

export function useRemoveDatabase(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (dbid: string) => databasesApi.remove(appId, dbid),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.databases(appId) })
      qc.invalidateQueries({ queryKey: queryKeys.apps.env(appId) })
    },
  })
}
