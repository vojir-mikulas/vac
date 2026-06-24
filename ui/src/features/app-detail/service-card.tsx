import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Cog, Play, RotateCw, ScrollText, ShieldAlert, Square } from 'lucide-react'
import { toast } from 'sonner'

import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { CopyButton } from '@/components/common/copy-button'
import {
  useRestartService,
  useStartService,
  useStopService,
  useUpdateService,
} from '@/lib/api/services'
import { guestAccessApi } from '@/lib/api/guest-access'
import { queryKeys } from '@/lib/query/keys'
import { useInstanceInfo } from '@/lib/api/instance'
import { ShellDialog } from '@/features/app-detail/shell-dialog'
import { useAppStatsContext } from '@/features/app-detail/stats-context'
import { formatBytes, formatDuration, formatPercent } from '@/lib/format'
import type { Service } from '@/types/api'

export function ServiceCard({
  appId,
  service,
  noBackupWarning,
}: {
  appId: string
  service: Service
  noBackupWarning?: boolean
}) {
  const { t } = useTranslation('app-detail')
  const stats = useAppStatsContext()
  const live = stats[service.name]
  const restart = useRestartService(appId)
  const stop = useStopService(appId)
  const start = useStartService(appId)
  const { data: instance } = useInstanceInfo()

  const stopped = service.status === 'stopped'
  const running = service.status === 'running'
  const busy = restart.isPending || stop.isPending || start.isPending

  return (
    <Card className="gap-0 p-0">
      <div className="flex items-center justify-between gap-2 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm font-semibold">{service.name}</span>
          <StatusPill status={service.status} size="sm" />
          {noBackupWarning ? (
            <span
              className="inline-flex items-center gap-1 rounded-full border border-warn-border bg-warn-bg px-2 py-0.5 text-2xs font-medium text-warn-foreground"
              title={t('serviceCard.noBackupTitle')}
            >
              <ShieldAlert className="size-3" />
              {t('serviceCard.noBackup')}
            </span>
          ) : null}
        </div>
        <div className="flex gap-1">
          <ConfigureDialog appId={appId} service={service} />
          {instance?.enable_shell && running ? (
            <ShellDialog appId={appId} service={service.name} />
          ) : null}
          <Button variant="ghost" size="sm" asChild>
            <Link to="/apps/$appId/logs" params={{ appId }} search={{ service: service.name }}>
              <ScrollText className="size-3.5" />
              {t('serviceCard.viewLogs')}
            </Link>
          </Button>
          {stopped ? (
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={() =>
                start.mutate(service.name, {
                  onSuccess: () =>
                    toast.success(t('serviceCard.starting', { service: service.name })),
                  onError: (e) => toast.error(e.message),
                })
              }
            >
              <Play className="size-3.5" />
              {t('serviceCard.start')}
            </Button>
          ) : (
            <>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() =>
                  restart.mutate(service.name, {
                    onSuccess: () =>
                      toast.success(t('serviceCard.restarting', { service: service.name })),
                    onError: (e) => toast.error(e.message),
                  })
                }
              >
                <RotateCw className="size-3.5" />
                {t('serviceCard.restart')}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() =>
                  stop.mutate(service.name, {
                    onSuccess: () =>
                      toast.success(t('serviceCard.stopping', { service: service.name })),
                    onError: (e) => toast.error(e.message),
                  })
                }
              >
                <Square className="size-3.5" />
                {t('serviceCard.stop')}
              </Button>
            </>
          )}
        </div>
      </div>

      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 px-5 py-4 sm:grid-cols-4">
        <Metric label={t('serviceCard.cpu')} value={live ? formatPercent(live.cpu_percent) : '—'} />
        <Metric label={t('serviceCard.memory')} value={live ? formatBytes(live.mem_bytes) : '—'} />
        <Metric
          label={t('serviceCard.uptime')}
          value={live ? formatDuration(live.uptime_seconds) : '—'}
        />
        <Metric label={t('serviceCard.restarts')} value={String(service.restart_count)} />
      </dl>

      <div className="flex flex-wrap gap-x-6 gap-y-1 border-t px-5 py-3 font-mono text-2xs text-muted-foreground">
        <span>
          {t('serviceCard.port', { port: service.exposed_port ?? service.internal_port ?? '—' })}
        </span>
        <span>{t('serviceCard.health', { path: service.health_path ?? '—' })}</span>
        {service.last_exit_code != null ? (
          <span className="text-err-foreground">
            {t('serviceCard.exit', { code: service.last_exit_code })}
          </span>
        ) : null}
        {service.oom_killed_count > 0 ? (
          <span className="text-err-foreground" title={t('serviceCard.oomTitle')}>
            {t('serviceCard.oomKilled', { count: service.oom_killed_count })}
          </span>
        ) : null}
      </div>
    </Card>
  )
}

// GuestAccessControls manages this service's shared access code: set/rotate,
// reveal+copy, and remove. The code lets non-operators past the VAC login gate
// for THIS service only — for sharing one internal tool with friends. Operates
// on its own endpoints, separate from the service-config Save button.
function GuestAccessControls({ appId, service }: { appId: string; service: Service }) {
  const { t } = useTranslation('app-detail')
  const qc = useQueryClient()
  const [enabled, setEnabled] = useState(service.guest_access_enabled)
  const [draft, setDraft] = useState('')
  const [revealed, setRevealed] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const refreshServices = () => qc.invalidateQueries({ queryKey: queryKeys.apps.services(appId) })

  const save = async () => {
    const code = draft.trim()
    if (code.length < 6) {
      toast.error(t('guestAccess.tooShort', { min: 6 }))
      return
    }
    setBusy(true)
    try {
      await guestAccessApi.set(appId, service.name, code)
      setEnabled(true)
      setDraft('')
      setRevealed(null)
      void refreshServices()
      toast.success(t('guestAccess.saved'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('guestAccess.revealFailed'))
    } finally {
      setBusy(false)
    }
  }

  const reveal = async () => {
    setBusy(true)
    try {
      const { code } = await guestAccessApi.reveal(appId, service.name)
      setRevealed(code)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('guestAccess.revealFailed'))
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    setBusy(true)
    try {
      await guestAccessApi.remove(appId, service.name)
      setEnabled(false)
      setRevealed(null)
      void refreshServices()
      toast.success(t('guestAccess.disabled'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t('guestAccess.revealFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid gap-2 rounded-md border border-border/60 bg-muted/30 p-3">
      <Label className="text-xs font-medium">{t('guestAccess.title')}</Label>
      <p className="text-2xs text-muted-foreground">{t('guestAccess.intro')}</p>
      <p className="text-2xs text-muted-foreground">
        {enabled ? t('guestAccess.statusSet') : t('guestAccess.statusNone')}
      </p>

      {enabled && revealed ? (
        <div className="grid gap-1.5 rounded-md border border-brand/40 bg-brand/5 p-2">
          <Label className="text-2xs font-medium">{t('guestAccess.currentCode')}</Label>
          <div className="flex items-center gap-2">
            <Input readOnly value={revealed} className="font-mono text-xs" />
            <CopyButton value={revealed} />
          </div>
        </div>
      ) : null}

      <div className="flex items-center gap-2">
        <Input
          type="text"
          autoComplete="off"
          placeholder={t('guestAccess.placeholder')}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="font-mono text-xs"
        />
        <Button variant="ghost" size="sm" onClick={() => setDraft(randomCode())}>
          {t('guestAccess.generate')}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button variant="brand" size="sm" disabled={busy} onClick={save}>
          {enabled ? t('guestAccess.rotate') : t('guestAccess.save')}
        </Button>
        {enabled ? (
          <>
            <Button variant="outline" size="sm" disabled={busy} onClick={reveal}>
              {t('guestAccess.reveal')}
            </Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={remove}>
              {t('guestAccess.disable')}
            </Button>
          </>
        ) : null}
      </div>
    </div>
  )
}

// randomCode builds a short, human-shareable code from an unambiguous alphabet
// (no 0/O/1/l). Client-side convenience; the backend enforces the minimum length.
function randomCode(): string {
  const alphabet = 'abcdefghijkmnpqrstuvwxyz23456789'
  const bytes = new Uint8Array(10)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-2xs uppercase tracking-wider text-muted-foreground">{label}</dt>
      <dd className="font-mono text-sm tabular-nums">{value}</dd>
    </div>
  )
}

function ConfigureDialog({ appId, service }: { appId: string; service: Service }) {
  const { t } = useTranslation('app-detail')
  const [open, setOpen] = useState(false)
  const [internalPort, setInternalPort] = useState(service.internal_port?.toString() ?? '')
  const [healthPath, setHealthPath] = useState(service.health_path ?? '')
  const [isPrivate, setIsPrivate] = useState(service.is_private)
  const [requiresAuth, setRequiresAuth] = useState(service.requires_auth)
  const update = useUpdateService(appId)

  const submit = () => {
    update.mutate(
      {
        name: service.name,
        input: {
          internal_port: internalPort ? Number(internalPort) : undefined,
          health_path: healthPath || undefined,
          is_private: isPrivate,
          // A private service has no route to guard, so never send a stale true.
          requires_auth: isPrivate ? false : requiresAuth,
        },
      },
      {
        onSuccess: () => {
          toast.success(t('serviceCard.serviceUpdated'))
          setOpen(false)
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm">
          <Cog className="size-3.5" />
          {t('serviceCard.configure')}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('serviceCard.configureTitle', { service: service.name })}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="grid gap-2">
            <Label>{t('serviceCard.exposedPort')}</Label>
            <p className="font-mono text-sm tabular-nums">
              {service.exposed_port ?? t('serviceCard.exposedPortNone')}
            </p>
            <p className="text-xs text-muted-foreground">{t('serviceCard.exposedPortHint')}</p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="internal">{t('serviceCard.internalPort')}</Label>
            <Input
              id="internal"
              inputMode="numeric"
              value={internalPort}
              onChange={(e) => setInternalPort(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t('serviceCard.internalPortHint')}</p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="health">{t('serviceCard.healthPath')}</Label>
            <Input
              id="health"
              placeholder="/"
              value={healthPath}
              onChange={(e) => setHealthPath(e.target.value)}
            />
          </div>
          <div className="flex items-start justify-between gap-4">
            <div className="grid gap-1">
              <Label htmlFor="private">{t('serviceCard.private')}</Label>
              <p className="text-xs text-muted-foreground">{t('serviceCard.privateHint')}</p>
            </div>
            <Switch id="private" checked={isPrivate} onCheckedChange={setIsPrivate} />
          </div>
          <div className="flex items-start justify-between gap-4">
            <div className="grid gap-1">
              <Label htmlFor="requires-auth">{t('serviceCard.requireAuth')}</Label>
              <p className="text-xs text-muted-foreground">{t('serviceCard.requireAuthHint')}</p>
            </div>
            {/* A private service has no public route to put behind the login. */}
            <Switch
              id="requires-auth"
              checked={!isPrivate && requiresAuth}
              disabled={isPrivate}
              onCheckedChange={setRequiresAuth}
            />
          </div>

          {/* Shared access code for this service — only meaningful once the gate
              is on. Its own endpoints, independent of the Save button below. */}
          {!isPrivate && requiresAuth ? (
            <GuestAccessControls appId={appId} service={service} />
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="brand" disabled={update.isPending} onClick={submit}>
            {t('common.saveChanges')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
