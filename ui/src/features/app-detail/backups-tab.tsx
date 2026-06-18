import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import {
  Database,
  Download,
  Pencil,
  Play,
  Plus,
  RotateCcw,
  ShieldCheck,
  Trash2,
} from 'lucide-react'
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
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
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
  backupsApi,
  useBackups,
  useCreateBackup,
  useDeleteBackup,
  useRunBackup,
  useUpdateBackup,
  useBackupRuns,
  useBackupRestores,
  useRestoreBackup,
  useBackupVerifications,
  useVerifyBackup,
} from '@/lib/api/backups'
import { useServices } from '@/lib/api/services'
import { formatBackupSize, scheduleSummary, type ScheduleLabels } from '@/lib/backups'
import type {
  BackupConfig,
  BackupConfigInput,
  BackupFrequency,
  BackupRun,
  VerificationRun,
} from '@/types/api'

type AppDetailT = ReturnType<typeof useTranslation<'app-detail'>>['t']

// 0–6 mirrors JS getDay() / the backend's day_of_week; the translation keys
// backups.days.0…6 carry the localized weekday names.
const DAY_INDEXES = [0, 1, 2, 3, 4, 5, 6] as const

// Day-of-week (0=Sunday … 6=Saturday) → its catalog key, kept as a literal tuple
// so t() stays type-safe (a `backups.days.${number}` template would be too wide).
const DAY_KEYS = [
  'backups.days.0',
  'backups.days.1',
  'backups.days.2',
  'backups.days.3',
  'backups.days.4',
  'backups.days.5',
  'backups.days.6',
] as const

// scheduleLabels binds the shared scheduleSummary helper to this namespace's keys.
function scheduleLabels(t: AppDetailT): ScheduleLabels {
  return {
    weekly: (v) => t('backups.weeklySummary', v),
    daily: (v) => t('backups.dailySummary', v),
    dayName: (i) => t(DAY_KEYS[i] ?? DAY_KEYS[0]),
  }
}

export function BackupsTab({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: configs, isLoading, isError, refetch } = useBackups(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">{t('backups.title')}</SectionHeader>
        <BackupDialog appId={appId} />
      </div>

      <SwapFade
        id={
          isLoading
            ? 'loading'
            : isError
              ? 'error'
              : configs && configs.length > 0
                ? 'cards'
                : 'empty'
        }
      >
        {isLoading ? (
          <CardStackSkeleton count={1} rowHeight="h-36" />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : configs && configs.length > 0 ? (
          <div className="flex flex-col gap-4">
            {configs.map((c) => (
              <BackupCard key={c.id} appId={appId} config={c} />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={Database}
            title={t('backups.emptyTitle')}
            description={t('backups.emptyDescription')}
          />
        )}
      </SwapFade>
    </div>
  )
}

function BackupCard({ appId, config }: { appId: string; config: BackupConfig }) {
  const { t } = useTranslation('app-detail')
  const [showRuns, setShowRuns] = useState(false)
  const run = useRunBackup(appId)
  const remove = useDeleteBackup(appId)

  return (
    <Card className="gap-0 overflow-hidden p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm font-semibold">{config.service_name}</span>
          {config.last_run ? (
            <StatusPill status={config.last_run.status} size="sm" />
          ) : (
            <StatusPill status="queued" size="sm" />
          )}
          {!config.enabled ? (
            <span className="text-2xs uppercase tracking-wider text-muted-foreground">
              {t('backups.paused')}
            </span>
          ) : null}
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={run.isPending}
            onClick={() =>
              run.mutate(config.id, {
                onSuccess: () => toast.success(t('backups.backupStarted')),
                onError: (e) => toast.error(e.message),
              })
            }
          >
            <Play className="size-3.5" />
            {t('backups.backupNow')}
          </Button>
          {config.verifiable ? <VerifyControl appId={appId} config={config} /> : null}
          <BackupDialog appId={appId} config={config} />
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="danger" size="sm" disabled={remove.isPending}>
                <Trash2 className="size-3.5" />
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('backups.deleteDialogTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('backups.confirmDelete', { service: config.service_name })}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() =>
                    remove.mutate(config.id, {
                      onSuccess: () => toast.success(t('backups.configDeleted')),
                      onError: (e) => toast.error(e.message),
                    })
                  }
                  disabled={remove.isPending}
                  className="bg-err text-err-foreground hover:bg-err/90"
                >
                  {t('common.delete')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </div>

      <div className="grid gap-x-6 gap-y-1.5 px-5 py-4 text-sm sm:grid-cols-2">
        <Field
          label={t('backups.fieldSchedule')}
          value={scheduleSummary(config, scheduleLabels(t))}
        />
        <Field
          label={t('backups.fieldDestination')}
          value={
            config.destination === 's3' ? t('backups.destinationS3') : t('backups.destinationLocal')
          }
        />
        <Field
          label={t('backups.fieldKeep')}
          value={t('backups.keepValue', { count: config.keep_count })}
        />
        <Field
          label={t('backups.fieldLastRun')}
          value={
            config.last_run?.finished_at
              ? `${new Date(config.last_run.finished_at).toLocaleString()} · ${formatBackupSize(config.last_run.size_bytes)}`
              : t('backups.lastRunNever')
          }
        />
        <div className="sm:col-span-2">
          <div className="text-2xs uppercase tracking-wider text-muted-foreground">
            {t('backups.command')}
          </div>
          <code className="mt-0.5 block truncate font-mono text-xs">{config.command}</code>
        </div>
      </div>

      <div className="border-t px-5 py-3">
        <button
          type="button"
          className="text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={() => setShowRuns((s) => !s)}
        >
          {showRuns ? t('backups.hideRunHistory') : t('backups.showRunHistory')}
        </button>
        {showRuns ? <RunHistory appId={appId} config={config} /> : null}
      </div>
    </Card>
  )
}

// VerifyControl is the per-config restorability check: a status badge for the
// latest verification plus a button to run one now. Non-destructive (it restores
// into a throwaway scratch DB), so no confirmation gate. Polls while a check runs
// so the badge flips running → verified/failed without a reload.
function VerifyControl({ appId, config }: { appId: string; config: BackupConfig }) {
  const { t } = useTranslation('app-detail')
  const { data: history } = useBackupVerifications(appId, config.id, config.verifiable)
  const verify = useVerifyBackup(appId, config.id)
  const latest = history?.[0] ?? config.last_verification ?? null
  const running = latest?.status === 'running'
  return (
    <div className="flex items-center gap-2">
      {latest ? <VerifyBadge v={latest} /> : null}
      <Button
        variant="outline"
        size="sm"
        disabled={verify.isPending || running}
        onClick={() =>
          verify.mutate(undefined, {
            onSuccess: () => toast.success(t('backups.verify.started')),
            onError: (e) => toast.error(e.message),
          })
        }
      >
        <ShieldCheck className="size-3.5" />
        {t('backups.verify.action')}
      </Button>
    </div>
  )
}

// VerifyBadge shows the latest restorability-check outcome, with the time (or the
// failure reason) in a tooltip.
function VerifyBadge({ v }: { v: VerificationRun }) {
  const { t } = useTranslation('app-detail')
  const label =
    v.status === 'success'
      ? t('backups.verify.verified')
      : v.status === 'failed'
        ? t('backups.verify.failed')
        : t('backups.verify.checking')
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex cursor-help items-center gap-1.5 text-2xs text-muted-foreground">
          <StatusPill status={v.status} size="sm" />
          {label}
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {v.error
          ? v.error
          : t('backups.verify.checkedAt', { when: new Date(v.started_at).toLocaleString() })}
      </TooltipContent>
    </Tooltip>
  )
}

function RunHistory({ appId, config }: { appId: string; config: BackupConfig }) {
  const { t } = useTranslation('app-detail')
  const { data: runs, isLoading } = useBackupRuns(appId, config.id)
  const { data: restores } = useBackupRestores(appId, config.id)
  const lastRestore = restores?.[0]

  if (isLoading) return <Skeleton className="mt-3 h-24 w-full rounded-lg" />
  if (!runs || runs.length === 0) {
    return (
      <p className="mt-3 text-sm text-muted-foreground">{t('backups.runHistoryLoadingNone')}</p>
    )
  }

  return (
    <div className="mt-3 flex flex-col gap-2">
      {lastRestore ? (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span>{t('backups.restore.lastRestore')}</span>
          <StatusPill status={lastRestore.status} size="sm" />
          <span>{new Date(lastRestore.started_at).toLocaleString()}</span>
          {lastRestore.error ? (
            <span className="text-err-foreground" title={lastRestore.error}>
              {lastRestore.error.length > 48
                ? lastRestore.error.slice(0, 48) + '…'
                : lastRestore.error}
            </span>
          ) : null}
        </div>
      ) : null}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('backups.runStarted')}</TableHead>
            <TableHead>{t('backups.runStatus')}</TableHead>
            <TableHead>{t('backups.runSize')}</TableHead>
            <TableHead className="text-right">{t('backups.runArtifact')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            <TableRow key={r.id}>
              <TableCell className="text-xs">{new Date(r.started_at).toLocaleString()}</TableCell>
              <TableCell>
                <StatusPill status={r.status} size="sm" />
                {r.error ? (
                  <span className="ml-2 text-xs text-err-foreground" title={r.error}>
                    {r.error.length > 48 ? r.error.slice(0, 48) + '…' : r.error}
                  </span>
                ) : null}
              </TableCell>
              <TableCell className="text-xs">{formatBackupSize(r.size_bytes)}</TableCell>
              <TableCell className="text-right">
                {r.status === 'success' ? (
                  <div className="inline-flex items-center gap-3">
                    <a
                      className="inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline"
                      href={backupsApi.downloadUrl(appId, r.id)}
                      download
                    >
                      <Download className="size-3.5" />
                      {t('backups.download')}
                    </a>
                    <RestoreAction appId={appId} config={config} run={r} />
                  </div>
                ) : (
                  <span className="text-xs text-muted-foreground">—</span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// RestoreAction is the per-run Restore affordance. For a config VAC can't restore
// (custom backup command) it renders a disabled hint pointing at manual download;
// otherwise it opens a typed-confirmation dialog. The destructive POST is fronted
// by step-up 2FA server-side, handled transparently by the global StepUpProvider.
function RestoreAction({
  appId,
  config,
  run,
}: {
  appId: string
  config: BackupConfig
  run: BackupRun
}) {
  const { t } = useTranslation('app-detail')
  const [open, setOpen] = useState(false)
  const [phrase, setPhrase] = useState('')
  const restore = useRestoreBackup(appId, config.id)

  if (!config.restorable) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex cursor-help items-center gap-1 text-xs text-muted-foreground">
            <RotateCcw className="size-3.5" />
            {t('backups.restore.action')}
          </span>
        </TooltipTrigger>
        <TooltipContent>{t('backups.restore.unsupported')}</TooltipContent>
      </Tooltip>
    )
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) setPhrase('')
      }}
    >
      <button
        type="button"
        className="inline-flex items-center gap-1 text-xs font-medium text-err-foreground hover:underline"
        onClick={() => setOpen(true)}
      >
        <RotateCcw className="size-3.5" />
        {t('backups.restore.action')}
      </button>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('backups.restore.confirmTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            <Trans
              t={t}
              i18nKey="backups.restore.confirmDescription"
              values={{ service: config.service_name }}
              components={[<span className="font-mono font-semibold" />]}
            />
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Input
          value={phrase}
          onChange={(e) => setPhrase(e.target.value)}
          placeholder={config.service_name}
          className="font-mono"
          autoFocus
        />
        <AlertDialogFooter>
          <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={phrase !== config.service_name || restore.isPending}
            onClick={(e) => {
              e.preventDefault()
              if (phrase !== config.service_name) return
              restore.mutate(run.id, {
                onSuccess: () => {
                  toast.success(t('backups.restore.started'))
                  setOpen(false)
                  setPhrase('')
                },
                onError: (err) => toast.error(err.message),
              })
            }}
          >
            {restore.isPending
              ? t('backups.restore.restoring')
              : t('backups.restore.confirmAction')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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

function BackupDialog({ appId, config }: { appId: string; config?: BackupConfig }) {
  const { t } = useTranslation('app-detail')
  const isEdit = !!config
  const [open, setOpen] = useState(false)
  const { data: services } = useServices(appId)
  const create = useCreateBackup(appId)
  const update = useUpdateBackup(appId)
  const pending = create.isPending || update.isPending

  const [serviceName, setServiceName] = useState(config?.service_name ?? '')
  const [command, setCommand] = useState(
    config?.command ?? 'pg_dump -U $POSTGRES_USER $POSTGRES_DB',
  )
  const [frequency, setFrequency] = useState<BackupFrequency>(config?.frequency ?? 'daily')
  const [hour, setHour] = useState(config?.hour_of_day ?? 3)
  const [dayOfWeek, setDayOfWeek] = useState(config?.day_of_week ?? 0)
  const [destination, setDestination] = useState<'local' | 's3'>(config?.destination ?? 'local')
  const [keepCount, setKeepCount] = useState(config?.keep_count ?? 7)
  const [enabled, setEnabled] = useState(config?.enabled ?? true)
  const [s3, setS3] = useState({
    endpoint: '',
    region: 'us-east-1',
    bucket: '',
    access_key: '',
    secret_key: '',
    use_ssl: true,
    prefix: '',
  })

  const submit = () => {
    if (!serviceName) {
      toast.error(t('backups.dialog.pickService'))
      return
    }
    // On edit, an unchanged S3 destination with a blank secret means "keep the
    // existing credentials & settings": the backend preserves them when s3 is
    // null. Re-enter the secret to change any S3 field.
    const keepS3 = isEdit && destination === 's3' && s3.secret_key.trim() === ''
    const input: BackupConfigInput = {
      service_name: serviceName,
      command,
      frequency,
      hour_of_day: hour,
      day_of_week: frequency === 'weekly' ? dayOfWeek : null,
      destination,
      keep_count: keepCount,
      enabled,
      s3: destination === 's3' && !keepS3 ? s3 : null,
    }
    const onDone = {
      onSuccess: () => {
        toast.success(isEdit ? t('backups.dialog.updated') : t('backups.dialog.configured'))
        setOpen(false)
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (isEdit) {
      update.mutate({ cid: config.id, input }, onDone)
    } else {
      create.mutate(input, onDone)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {isEdit ? (
          <Button variant="outline" size="sm" aria-label={t('backups.dialog.editButton')}>
            <Pencil className="size-3.5" />
          </Button>
        ) : (
          <Button variant="brand" size="sm">
            <Plus className="size-4" />
            {t('backups.dialog.addButton')}
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isEdit
              ? t('backups.dialog.editTitle', { service: serviceName })
              : t('backups.dialog.newTitle')}
          </DialogTitle>
        </DialogHeader>

        <ScrollArea className="-mx-6 min-h-0 flex-1" viewportClassName="px-6">
          <div className="flex flex-col gap-4 py-2">
            {isEdit ? (
              <Labeled label={t('backups.dialog.service')}>
                <Input value={serviceName} disabled className="font-mono text-xs" />
              </Labeled>
            ) : (
              <Labeled label={t('backups.dialog.service')}>
                <Select value={serviceName} onValueChange={setServiceName}>
                  <SelectTrigger>
                    <SelectValue placeholder={t('backups.dialog.selectService')} />
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
            )}

            {isEdit ? (
              <label className="flex items-center justify-between gap-3">
                <span className="text-xs font-medium">{t('backups.dialog.enabled')}</span>
                <Switch checked={enabled} onCheckedChange={setEnabled} />
              </label>
            ) : null}

            <Labeled label={t('backups.dialog.command')} hint={t('backups.dialog.commandHint')}>
              <Textarea
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                rows={2}
                className="font-mono text-xs"
              />
            </Labeled>

            <div className="grid grid-cols-2 gap-4">
              <Labeled label={t('backups.dialog.frequency')}>
                <Select value={frequency} onValueChange={(v) => setFrequency(v as BackupFrequency)}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="daily">{t('backups.dialog.frequencyDaily')}</SelectItem>
                    <SelectItem value="weekly">{t('backups.dialog.frequencyWeekly')}</SelectItem>
                  </SelectContent>
                </Select>
              </Labeled>
              <Labeled label={t('backups.dialog.hour')}>
                <Input
                  type="number"
                  min={0}
                  max={23}
                  value={hour}
                  onChange={(e) => setHour(Number(e.target.value))}
                />
              </Labeled>
            </div>

            {frequency === 'weekly' ? (
              <Labeled label={t('backups.dialog.dayOfWeek')}>
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

            <div className="grid grid-cols-2 gap-4">
              <Labeled label={t('backups.dialog.destination')}>
                <Select
                  value={destination}
                  onValueChange={(v) => setDestination(v as 'local' | 's3')}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="local">{t('backups.dialog.destinationLocal')}</SelectItem>
                    <SelectItem value="s3">{t('backups.dialog.destinationS3')}</SelectItem>
                  </SelectContent>
                </Select>
              </Labeled>
              <Labeled label={t('backups.dialog.keepLastN')}>
                <Input
                  type="number"
                  min={1}
                  value={keepCount}
                  onChange={(e) => setKeepCount(Number(e.target.value))}
                />
              </Labeled>
            </div>

            {destination === 's3' ? (
              <div className="flex flex-col gap-3 rounded-lg border bg-surface-1 p-3">
                <div className="grid grid-cols-2 gap-3">
                  <Labeled label={t('backups.dialog.endpoint')}>
                    <Input
                      placeholder="s3.amazonaws.com"
                      value={s3.endpoint}
                      onChange={(e) => setS3({ ...s3, endpoint: e.target.value })}
                    />
                  </Labeled>
                  <Labeled label={t('backups.dialog.region')}>
                    <Input
                      value={s3.region}
                      onChange={(e) => setS3({ ...s3, region: e.target.value })}
                    />
                  </Labeled>
                </div>
                <Labeled label={t('backups.dialog.bucket')}>
                  <Input
                    value={s3.bucket}
                    onChange={(e) => setS3({ ...s3, bucket: e.target.value })}
                  />
                </Labeled>
                <div className="grid grid-cols-2 gap-3">
                  <Labeled label={t('backups.dialog.accessKey')}>
                    <Input
                      value={s3.access_key}
                      onChange={(e) => setS3({ ...s3, access_key: e.target.value })}
                    />
                  </Labeled>
                  <Labeled
                    label={t('backups.dialog.secretKey')}
                    hint={isEdit ? t('backups.dialog.secretKeyHint') : undefined}
                  >
                    <Input
                      type="password"
                      value={s3.secret_key}
                      placeholder={isEdit ? t('backups.dialog.secretKeyUnchanged') : undefined}
                      onChange={(e) => setS3({ ...s3, secret_key: e.target.value })}
                    />
                  </Labeled>
                </div>
              </div>
            ) : null}
          </div>
        </ScrollArea>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="brand" disabled={pending} onClick={submit}>
            {isEdit ? t('common.saveChanges') : t('backups.dialog.saveBackup')}
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
