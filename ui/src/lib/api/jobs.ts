import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { JobRun, ScheduledJob, ScheduledJobInput } from '@/types/api'

export const jobsApi = {
  list: (appId: string) => api.get<ScheduledJob[]>(`apps/${appId}/jobs`),
  create: (appId: string, input: ScheduledJobInput) =>
    api.post<ScheduledJob>(`apps/${appId}/jobs`, input),
  update: (appId: string, jid: string, input: ScheduledJobInput) =>
    api.put<ScheduledJob>(`apps/${appId}/jobs/${jid}`, input),
  remove: (appId: string, jid: string) => api.del<void>(`apps/${appId}/jobs/${jid}`),
  run: (appId: string, jid: string) => api.post<void>(`apps/${appId}/jobs/${jid}/run`),
  runs: (appId: string, jid: string) => api.get<JobRun[]>(`apps/${appId}/jobs/${jid}/runs`),
}

export function useAppJobs(appId: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.jobs(appId),
    queryFn: () => jobsApi.list(appId),
    enabled,
  })
}

export function useCreateJob(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: ScheduledJobInput) => jobsApi.create(appId, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.jobs(appId) }),
  })
}

export function useUpdateJob(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ jid, input }: { jid: string; input: ScheduledJobInput }) =>
      jobsApi.update(appId, jid, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.jobs(appId) }),
  })
}

export function useDeleteJob(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (jid: string) => jobsApi.remove(appId, jid),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.apps.jobs(appId) }),
  })
}

export function useRunJob(appId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (jid: string) => jobsApi.run(appId, jid),
    // The run is detached server-side; refetch shortly so the history and the
    // last-run pill catch up.
    onSuccess: (_, jid) => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.jobRuns(appId, jid) })
      setTimeout(() => {
        qc.invalidateQueries({ queryKey: queryKeys.apps.jobs(appId) })
        qc.invalidateQueries({ queryKey: queryKeys.apps.jobRuns(appId, jid) })
      }, 2_000)
    },
  })
}

// useJobRuns polls run history while a run is in flight so the status pill flips
// from running → terminal without a reload.
export function useJobRuns(appId: string, jid: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.apps.jobRuns(appId, jid),
    queryFn: () => jobsApi.runs(appId, jid),
    enabled,
    refetchInterval: (q) =>
      (q.state.data ?? []).some((r) => r.status === 'running') ? 2_000 : false,
  })
}
