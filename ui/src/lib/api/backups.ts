import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type {
  BackupConfig,
  BackupConfigInput,
  BackupRun,
  FleetBackups,
  RestoreRun,
} from '@/types/api'

export const backupsApi = {
  list: (appId: string) => api.get<BackupConfig[]>(`apps/${appId}/backups`),
  // Box-wide overview: every app's configs + a health summary. Read-only.
  fleet: () => api.get<FleetBackups>('backups'),
  create: (appId: string, input: BackupConfigInput) =>
    api.post<BackupConfig>(`apps/${appId}/backups`, input),
  update: (appId: string, cid: string, input: BackupConfigInput) =>
    api.put<BackupConfig>(`apps/${appId}/backups/${cid}`, input),
  remove: (appId: string, cid: string) => api.del<void>(`apps/${appId}/backups/${cid}`),
  run: (appId: string, cid: string) => api.post<void>(`apps/${appId}/backups/${cid}/run`),
  runs: (appId: string, cid: string) => api.get<BackupRun[]>(`apps/${appId}/backups/${cid}/runs`),
  // Artifact download is a plain authenticated GET — used as an anchor href.
  downloadUrl: (appId: string, rid: string) => `/api/apps/${appId}/backups/runs/${rid}/download`,
  // Restore replays a recorded run back into its container. Destructive — the
  // server fronts it with step-up 2FA (a 403 step_up_required is handled
  // transparently by the global StepUpProvider in lib/api/client.ts).
  restore: (appId: string, rid: string) =>
    api.post<void>(`apps/${appId}/backups/runs/${rid}/restore`),
  restores: (appId: string, cid: string) =>
    api.get<RestoreRun[]>(`apps/${appId}/backups/${cid}/restores`),
}

export function useBackups(appId: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.backups(appId),
    queryFn: () => backupsApi.list(appId),
    enabled,
  })
}

export function useCreateBackup(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: BackupConfigInput) => backupsApi.create(appId, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.backups(appId) }),
  })
}

export function useUpdateBackup(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ cid, input }: { cid: string; input: BackupConfigInput }) =>
      backupsApi.update(appId, cid, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.backups(appId) }),
  })
}

export function useDeleteBackup(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (cid: string) => backupsApi.remove(appId, cid),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.backups(appId) }),
  })
}

export function useRunBackup(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (cid: string) => backupsApi.run(appId, cid),
    // The run is detached server-side; refetch shortly so the history catches up.
    onSuccess: (_, cid) => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.backupRuns(appId, cid) })
      setTimeout(() => {
        qc.invalidateQueries({ queryKey: queryKeys.apps.backups(appId) })
        qc.invalidateQueries({ queryKey: queryKeys.apps.backupRuns(appId, cid) })
      }, 2_000)
    },
  })
}

export function useFleetBackups(enabled = true) {
  return useQuery({
    queryKey: queryKeys.backups.fleet,
    queryFn: () => backupsApi.fleet(),
    enabled,
    // The overview is a health dashboard — refresh periodically so a failed
    // nightly run or a freshly-finished manual run surfaces without a reload.
    refetchInterval: 30_000,
  })
}

// useRunFleetBackup triggers a manual run from the overview, where the app isn't
// fixed at hook-call time (each row carries its own app_id). Invalidates the
// fleet query so the row's last-run pill catches up.
export function useRunFleetBackup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ appId, cid }: { appId: string; cid: string }) => backupsApi.run(appId, cid),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.backups.fleet })
      // The run is detached server-side; refetch shortly so the status catches up.
      setTimeout(() => qc.invalidateQueries({ queryKey: queryKeys.backups.fleet }), 2_000)
    },
  })
}

// useBackupAttention collapses the overview into a single sidebar badge signal:
// how many backups failed in the last 7 days. Reuses the fleet query — no extra
// request beyond what the page already polls. Gated on the same managed-services
// flag as the page (pass enabled=false to skip the request entirely).
export function useBackupAttention(enabled = true): { count: number } {
  const { data } = useFleetBackups(enabled)
  return { count: data?.summary.failed_last_7d ?? 0 }
}

export function useBackupRuns(appId: string, cid: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.backupRuns(appId, cid),
    queryFn: () => backupsApi.runs(appId, cid),
    enabled,
  })
}

// useBackupRestores polls restore history while a restore is running so the
// status pill flips from running → success/failed without a reload.
export function useBackupRestores(appId: string, cid: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.backupRestores(appId, cid),
    queryFn: () => backupsApi.restores(appId, cid),
    enabled,
    refetchInterval: (q) =>
      (q.state.data ?? []).some((r) => r.status === 'running') ? 2_000 : false,
  })
}

export function useRestoreBackup(appId: string, cid: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (runId: string) => backupsApi.restore(appId, runId),
    // The restore is detached server-side; refetch the restore history so the
    // running pill appears, then again shortly after to catch the terminal state.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.backupRestores(appId, cid) })
      setTimeout(
        () => qc.invalidateQueries({ queryKey: queryKeys.apps.backupRestores(appId, cid) }),
        2_000,
      )
    },
  })
}
