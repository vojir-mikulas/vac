import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Clock, Pencil, Play, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { StatusPill } from '@/components/common/status-pill'
import { CardStackSkeleton } from '@/components/common/card-stack-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  useAppJobs,
  useCreateJob,
  useDeleteJob,
  useJobRuns,
  useRunJob,
  useUpdateJob,
} from '@/lib/api/jobs'
import { useServices } from '@/lib/api/services'
import { scheduleSummary, type ScheduleLabels } from '@/lib/backups'
import { relativeTime } from '@/lib/format'
import type { JobFrequency, JobRun, ScheduledJob, ScheduledJobInput } from '@/types/api'

type AppDetailT = ReturnType<typeof useTranslation<'app-detail'>>['t']

const DAY_INDEXES = [0, 1, 2, 3, 4, 5, 6] as const

// scheduleLabels binds the shared scheduleSummary helper to this namespace's
// keys, including the interval label that backups don't use.
function scheduleLabels(t: AppDetailT): ScheduleLabels {
  return {
    weekly: (v) => t('backups.weeklySummary', v),
    daily: (v) => t('backups.dailySummary', v),
    interval: (v) => t('jobs.intervalSummary', v),
    dayName: (i) => t(`backups.days.${(i % 7) as 0 | 1 | 2 | 3 | 4 | 5 | 6}`),
  }
}

export function JobsTab({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: jobs, isLoading, isError, refetch } = useAppJobs(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">{t('jobs.title')}</SectionHeader>
        <JobDialog appId={appId} />
      </div>

      <SwapFade
        id={isLoading ? 'loading' : isError ? 'error' : jobs && jobs.length > 0 ? 'cards' : 'empty'}
      >
        {isLoading ? (
          <CardStackSkeleton count={1} rowHeight="h-36" />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : jobs && jobs.length > 0 ? (
          <div className="flex flex-col gap-4">
            {jobs.map((j) => (
              <JobCard key={j.id} appId={appId} job={j} />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={Clock}
            title={t('jobs.emptyTitle')}
            description={t('jobs.emptyDescription')}
          />
        )}
      </SwapFade>
    </div>
  )
}

function JobCard({ appId, job }: { appId: string; job: ScheduledJob }) {
  const { t } = useTranslation('app-detail')
  const [showRuns, setShowRuns] = useState(false)
  const run = useRunJob(appId)
  const remove = useDeleteJob(appId)

  return (
    <Card className="gap-0 overflow-hidden p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold">{job.name}</span>
          <span className="font-mono text-xs text-muted-foreground">{job.service_name}</span>
          {job.last_run ? (
            <StatusPill status={job.last_run.status} size="sm" />
          ) : (
            <StatusPill status="queued" size="sm" />
          )}
          {!job.enabled ? (
            <span className="text-2xs uppercase tracking-wider text-muted-foreground">
              {t('jobs.paused')}
            </span>
          ) : null}
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={run.isPending}
            onClick={() =>
              run.mutate(job.id, {
                onSuccess: () => toast.success(t('jobs.runTriggered')),
                onError: (e) => toast.error(e.message),
              })
            }
          >
            <Play className="size-3.5" />
            {t('jobs.runNow')}
          </Button>
          <JobDialog appId={appId} job={job} />
          <Button
            variant="danger"
            size="sm"
            disabled={remove.isPending}
            onClick={() => {
              if (!confirm(t('jobs.confirmDelete', { name: job.name }))) return
              remove.mutate(job.id, {
                onSuccess: () => toast.success(t('jobs.jobDeleted')),
                onError: (e) => toast.error(e.message),
              })
            }}
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      <div className="grid gap-x-6 gap-y-1.5 px-5 py-4 text-sm sm:grid-cols-2">
        <Field label={t('jobs.fieldSchedule')} value={scheduleSummary(job, scheduleLabels(t))} />
        <Field
          label={t('jobs.fieldTimeout')}
          value={t('jobs.timeoutValue', { minutes: Math.round(job.timeout_seconds / 60) })}
        />
        <Field
          label={t('jobs.fieldLastRun')}
          value={job.last_run_at ? relativeTime(job.last_run_at) : t('jobs.lastRunNever')}
        />
        <Field
          label={t('jobs.fieldNextRun')}
          value={job.enabled && job.next_run_at ? relativeTime(job.next_run_at) : '—'}
        />
        <div className="sm:col-span-2">
          <div className="text-2xs uppercase tracking-wider text-muted-foreground">
            {t('jobs.command')}
          </div>
          <code className="mt-0.5 block truncate font-mono text-xs">{job.command}</code>
        </div>
      </div>

      <div className="border-t px-5 py-3">
        <button
          type="button"
          className="text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={() => setShowRuns((s) => !s)}
        >
          {showRuns ? t('jobs.hideRunHistory') : t('jobs.showRunHistory')}
        </button>
        {showRuns ? <RunHistory appId={appId} job={job} /> : null}
      </div>
    </Card>
  )
}

function RunHistory({ appId, job }: { appId: string; job: ScheduledJob }) {
  const { t } = useTranslation('app-detail')
  const { data: runs, isLoading } = useJobRuns(appId, job.id)

  if (isLoading) return <Skeleton className="mt-3 h-24 w-full rounded-lg" />
  if (!runs || runs.length === 0) {
    return <p className="mt-3 text-sm text-muted-foreground">{t('jobs.runHistoryNone')}</p>
  }

  return (
    <div className="mt-3">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('jobs.runStarted')}</TableHead>
            <TableHead>{t('jobs.runStatus')}</TableHead>
            <TableHead>{t('jobs.runExit')}</TableHead>
            <TableHead className="text-right">{t('jobs.runOutput')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function RunRow({ run }: { run: JobRun }) {
  const { t } = useTranslation('app-detail')
  const [open, setOpen] = useState(false)
  const detail = run.output || run.error
  return (
    <>
      <TableRow>
        <TableCell className="text-xs">{new Date(run.started_at).toLocaleString()}</TableCell>
        <TableCell>
          <StatusPill status={run.status} size="sm" />
        </TableCell>
        <TableCell className="font-mono text-xs">{run.exit_code ?? '—'}</TableCell>
        <TableCell className="text-right">
          {detail ? (
            <button
              type="button"
              className="text-xs font-medium text-brand hover:underline"
              onClick={() => setOpen((o) => !o)}
            >
              {open ? t('jobs.hideOutput') : t('jobs.showOutput')}
            </button>
          ) : (
            <span className="text-xs text-muted-foreground">—</span>
          )}
        </TableCell>
      </TableRow>
      {open && detail ? (
        <TableRow>
          <TableCell colSpan={4} className="p-0">
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words bg-surface-1 px-4 py-3 font-mono text-2xs text-muted-foreground">
              {run.error ? `${run.error}\n\n` : ''}
              {run.output ?? ''}
            </pre>
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-2xs uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className="mt-0.5">{value}</div>
    </div>
  )
}

function JobDialog({ appId, job }: { appId: string; job?: ScheduledJob }) {
  const { t } = useTranslation('app-detail')
  const isEdit = !!job
  const [open, setOpen] = useState(false)
  const { data: services } = useServices(appId)
  const create = useCreateJob(appId)
  const update = useUpdateJob(appId)
  const pending = create.isPending || update.isPending

  const [name, setName] = useState(job?.name ?? '')
  const [serviceName, setServiceName] = useState(job?.service_name ?? '')
  const [command, setCommand] = useState(job?.command ?? '')
  const [frequency, setFrequency] = useState<JobFrequency>(job?.frequency ?? 'daily')
  const [intervalMinutes, setIntervalMinutes] = useState(job?.interval_minutes ?? 15)
  const [hour, setHour] = useState(job?.hour_of_day ?? 3)
  const [dayOfWeek, setDayOfWeek] = useState(job?.day_of_week ?? 0)
  const [timeoutMinutes, setTimeoutMinutes] = useState(
    job ? Math.max(1, Math.round(job.timeout_seconds / 60)) : 30,
  )
  const [enabled, setEnabled] = useState(job?.enabled ?? true)

  const submit = () => {
    if (!name.trim()) {
      toast.error(t('jobs.dialog.nameRequired'))
      return
    }
    if (!serviceName) {
      toast.error(t('jobs.dialog.pickService'))
      return
    }
    if (!command.trim()) {
      toast.error(t('jobs.dialog.commandRequired'))
      return
    }
    const input: ScheduledJobInput = {
      name: name.trim(),
      service_name: serviceName,
      command,
      frequency,
      interval_minutes: frequency === 'interval' ? intervalMinutes : null,
      hour_of_day: hour,
      day_of_week: frequency === 'weekly' ? dayOfWeek : null,
      timeout_seconds: Math.max(1, timeoutMinutes) * 60,
      enabled,
    }
    const onDone = {
      onSuccess: () => {
        toast.success(isEdit ? t('jobs.dialog.updated') : t('jobs.dialog.created'))
        setOpen(false)
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (isEdit) {
      update.mutate({ jid: job.id, input }, onDone)
    } else {
      create.mutate(input, onDone)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {isEdit ? (
          <Button variant="outline" size="sm" aria-label={t('jobs.dialog.editButton')}>
            <Pencil className="size-3.5" />
          </Button>
        ) : (
          <Button variant="brand" size="sm">
            <Plus className="size-4" />
            {t('jobs.dialog.addButton')}
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t('jobs.dialog.editTitle', { name: job.name }) : t('jobs.dialog.newTitle')}
          </DialogTitle>
        </DialogHeader>

        <ScrollArea className="-mx-6 min-h-0 flex-1" viewportClassName="px-6">
          <div className="flex flex-col gap-4 py-2">
            <div className="grid grid-cols-2 gap-4">
              <Labeled label={t('jobs.dialog.name')}>
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t('jobs.dialog.namePlaceholder')}
                />
              </Labeled>
              <Labeled label={t('jobs.dialog.service')}>
                <Select value={serviceName} onValueChange={setServiceName}>
                  <SelectTrigger>
                    <SelectValue placeholder={t('jobs.dialog.selectService')} />
                  </SelectTrigger>
                  <SelectContent>
                    {(services ?? []).map((s) => (
                      <SelectItem key={s.id} value={s.name}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Labeled>
            </div>

            {isEdit ? (
              <label className="flex items-center justify-between gap-3">
                <span className="text-xs font-medium">{t('jobs.dialog.enabled')}</span>
                <Switch checked={enabled} onCheckedChange={setEnabled} />
              </label>
            ) : null}

            <Labeled label={t('jobs.dialog.command')} hint={t('jobs.dialog.commandHint')}>
              <Textarea
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                rows={2}
                placeholder="rails db:cleanup"
                className="font-mono text-xs"
              />
            </Labeled>

            <div className="grid grid-cols-2 gap-4">
              <Labeled label={t('jobs.dialog.frequency')}>
                <Select value={frequency} onValueChange={(v) => setFrequency(v as JobFrequency)}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="interval">{t('jobs.dialog.frequencyInterval')}</SelectItem>
                    <SelectItem value="daily">{t('jobs.dialog.frequencyDaily')}</SelectItem>
                    <SelectItem value="weekly">{t('jobs.dialog.frequencyWeekly')}</SelectItem>
                  </SelectContent>
                </Select>
              </Labeled>
              {frequency === 'interval' ? (
                <Labeled label={t('jobs.dialog.everyMinutes')}>
                  <Input
                    type="number"
                    min={1}
                    value={intervalMinutes}
                    onChange={(e) => setIntervalMinutes(Number(e.target.value))}
                  />
                </Labeled>
              ) : (
                <Labeled label={t('jobs.dialog.hour')}>
                  <Input
                    type="number"
                    min={0}
                    max={23}
                    value={hour}
                    onChange={(e) => setHour(Number(e.target.value))}
                  />
                </Labeled>
              )}
            </div>

            {frequency === 'weekly' ? (
              <Labeled label={t('jobs.dialog.dayOfWeek')}>
                <Select value={String(dayOfWeek)} onValueChange={(v) => setDayOfWeek(Number(v))}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {DAY_INDEXES.map((i) => (
                      <SelectItem key={i} value={String(i)}>
                        {t(`backups.days.${i}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Labeled>
            ) : null}

            <Labeled label={t('jobs.dialog.timeout')} hint={t('jobs.dialog.timeoutHint')}>
              <Input
                type="number"
                min={1}
                value={timeoutMinutes}
                onChange={(e) => setTimeoutMinutes(Number(e.target.value))}
              />
            </Labeled>
          </div>
        </ScrollArea>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="brand" disabled={pending} onClick={submit}>
            {isEdit ? t('common.saveChanges') : t('jobs.dialog.saveJob')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Labeled({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium">{label}</span>
      {children}
      {hint ? <span className="text-2xs text-muted-foreground">{hint}</span> : null}
    </label>
  )
}
