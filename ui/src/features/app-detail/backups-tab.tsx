import { useState } from 'react'
import { Database, Download, Play, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { EmptyState } from '@/components/common/empty-state'
import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
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
  backupsApi,
  useBackups,
  useCreateBackup,
  useDeleteBackup,
  useRunBackup,
  useBackupRuns,
} from '@/lib/api/backups'
import { useServices } from '@/lib/api/services'
import type { BackupConfig, BackupConfigInput, BackupFrequency } from '@/types/api'

const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

function formatBytes(n?: number | null): string {
  if (n == null) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

function scheduleSummary(c: BackupConfig): string {
  const at = `${String(c.hour_of_day).padStart(2, '0')}:00`
  if (c.frequency === 'weekly' && c.day_of_week != null) {
    return `Weekly on ${DAYS[c.day_of_week]} at ${at} UTC`
  }
  return `Daily at ${at} UTC`
}

export function BackupsTab({ appId }: { appId: string }) {
  const { data: configs, isLoading } = useBackups(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">Scheduled backups</SectionHeader>
        <AddBackupDialog appId={appId} />
      </div>

      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : configs && configs.length > 0 ? (
        <div className="flex flex-col gap-4">
          {configs.map((c) => (
            <BackupCard key={c.id} appId={appId} config={c} />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={Database}
          title="No backups configured"
          description="Schedule a dump command for a stateful service — VAC runs it in the container and ships the output to your chosen destination."
        />
      )}
    </div>
  )
}

function BackupCard({ appId, config }: { appId: string; config: BackupConfig }) {
  const [showRuns, setShowRuns] = useState(false)
  const run = useRunBackup(appId)
  const remove = useDeleteBackup(appId)

  return (
    <Card className="gap-0 p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm font-semibold">{config.service_name}</span>
          {config.last_run ? (
            <StatusPill status={config.last_run.status} size="sm" />
          ) : (
            <StatusPill status="queued" size="sm" />
          )}
          {!config.enabled ? (
            <span className="text-2xs uppercase tracking-wider text-muted-foreground">paused</span>
          ) : null}
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={run.isPending}
            onClick={() =>
              run.mutate(config.id, {
                onSuccess: () => toast.success('Backup started'),
                onError: (e) => toast.error(e.message),
              })
            }
          >
            <Play className="size-3.5" />
            Back up now
          </Button>
          <Button
            variant="danger"
            size="sm"
            disabled={remove.isPending}
            onClick={() => {
              if (!confirm(`Delete the backup config for ${config.service_name}?`)) return
              remove.mutate(config.id, {
                onSuccess: () => toast.success('Backup config deleted'),
                onError: (e) => toast.error(e.message),
              })
            }}
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      <div className="grid gap-x-6 gap-y-1.5 px-5 py-4 text-sm sm:grid-cols-2">
        <Field label="Schedule" value={scheduleSummary(config)} />
        <Field
          label="Destination"
          value={config.destination === 's3' ? 'S3-compatible' : 'Local volume'}
        />
        <Field label="Keep" value={`${config.keep_count} most recent`} />
        <Field
          label="Last run"
          value={
            config.last_run?.finished_at
              ? `${new Date(config.last_run.finished_at).toLocaleString()} · ${formatBytes(config.last_run.size_bytes)}`
              : 'never'
          }
        />
        <div className="sm:col-span-2">
          <div className="text-2xs uppercase tracking-wider text-muted-foreground">Command</div>
          <code className="mt-0.5 block truncate font-mono text-xs">{config.command}</code>
        </div>
      </div>

      <div className="border-t px-5 py-3">
        <button
          type="button"
          className="text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={() => setShowRuns((s) => !s)}
        >
          {showRuns ? 'Hide' : 'Show'} run history
        </button>
        {showRuns ? <RunHistory appId={appId} config={config} /> : null}
      </div>
    </Card>
  )
}

function RunHistory({ appId, config }: { appId: string; config: BackupConfig }) {
  const { data: runs, isLoading } = useBackupRuns(appId, config.id)

  if (isLoading) return <Skeleton className="mt-3 h-24 w-full rounded-lg" />
  if (!runs || runs.length === 0) {
    return <p className="mt-3 text-sm text-muted-foreground">No runs yet.</p>
  }

  return (
    <Table className="mt-3">
      <TableHeader>
        <TableRow>
          <TableHead>Started</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Size</TableHead>
          <TableHead className="text-right">Artifact</TableHead>
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
            <TableCell className="text-xs">{formatBytes(r.size_bytes)}</TableCell>
            <TableCell className="text-right">
              {r.status === 'success' ? (
                <a
                  className="inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline"
                  href={backupsApi.downloadUrl(appId, r.id)}
                  download
                >
                  <Download className="size-3.5" />
                  Download
                </a>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
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

function AddBackupDialog({ appId }: { appId: string }) {
  const [open, setOpen] = useState(false)
  const { data: services } = useServices(appId)
  const create = useCreateBackup(appId)

  const [serviceName, setServiceName] = useState('')
  const [command, setCommand] = useState('pg_dump -U $POSTGRES_USER $POSTGRES_DB')
  const [frequency, setFrequency] = useState<BackupFrequency>('daily')
  const [hour, setHour] = useState(3)
  const [dayOfWeek, setDayOfWeek] = useState(0)
  const [destination, setDestination] = useState<'local' | 's3'>('local')
  const [keepCount, setKeepCount] = useState(7)
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
      toast.error('Pick a service')
      return
    }
    const input: BackupConfigInput = {
      service_name: serviceName,
      command,
      frequency,
      hour_of_day: hour,
      day_of_week: frequency === 'weekly' ? dayOfWeek : null,
      destination,
      keep_count: keepCount,
      s3: destination === 's3' ? s3 : null,
    }
    create.mutate(input, {
      onSuccess: () => {
        toast.success('Backup configured')
        setOpen(false)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="brand" size="sm">
          <Plus className="size-4" />
          Add backup
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Schedule a backup</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <Labeled label="Service">
            <Select value={serviceName} onValueChange={setServiceName}>
              <SelectTrigger>
                <SelectValue placeholder="Select a service" />
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

          <Labeled
            label="Backup command"
            hint="Run inside the running container; container env vars (e.g. $POSTGRES_USER) are available."
          >
            <Textarea
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              rows={2}
              className="font-mono text-xs"
            />
          </Labeled>

          <div className="grid grid-cols-2 gap-4">
            <Labeled label="Frequency">
              <Select value={frequency} onValueChange={(v) => setFrequency(v as BackupFrequency)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="daily">Daily</SelectItem>
                  <SelectItem value="weekly">Weekly</SelectItem>
                </SelectContent>
              </Select>
            </Labeled>
            <Labeled label="Hour (UTC)">
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
            <Labeled label="Day of week">
              <Select value={String(dayOfWeek)} onValueChange={(v) => setDayOfWeek(Number(v))}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {DAYS.map((d, i) => (
                    <SelectItem key={d} value={String(i)}>
                      {d}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Labeled>
          ) : null}

          <div className="grid grid-cols-2 gap-4">
            <Labeled label="Destination">
              <Select
                value={destination}
                onValueChange={(v) => setDestination(v as 'local' | 's3')}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="local">Local volume</SelectItem>
                  <SelectItem value="s3">S3-compatible</SelectItem>
                </SelectContent>
              </Select>
            </Labeled>
            <Labeled label="Keep last N">
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
                <Labeled label="Endpoint">
                  <Input
                    placeholder="s3.amazonaws.com"
                    value={s3.endpoint}
                    onChange={(e) => setS3({ ...s3, endpoint: e.target.value })}
                  />
                </Labeled>
                <Labeled label="Region">
                  <Input
                    value={s3.region}
                    onChange={(e) => setS3({ ...s3, region: e.target.value })}
                  />
                </Labeled>
              </div>
              <Labeled label="Bucket">
                <Input
                  value={s3.bucket}
                  onChange={(e) => setS3({ ...s3, bucket: e.target.value })}
                />
              </Labeled>
              <div className="grid grid-cols-2 gap-3">
                <Labeled label="Access key">
                  <Input
                    value={s3.access_key}
                    onChange={(e) => setS3({ ...s3, access_key: e.target.value })}
                  />
                </Labeled>
                <Labeled label="Secret key">
                  <Input
                    type="password"
                    value={s3.secret_key}
                    onChange={(e) => setS3({ ...s3, secret_key: e.target.value })}
                  />
                </Labeled>
              </div>
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="brand" disabled={create.isPending} onClick={submit}>
            Save backup
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
