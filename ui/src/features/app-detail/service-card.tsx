import { useState } from 'react'
import { Cog, RotateCw, ShieldAlert } from 'lucide-react'
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
import { useRestartService, useUpdateService } from '@/lib/api/services'
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
  const stats = useAppStatsContext()
  const live = stats[service.name]
  const restart = useRestartService(appId)

  return (
    <Card className="gap-0 p-0">
      <div className="flex items-center justify-between gap-2 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm font-semibold">{service.name}</span>
          <StatusPill status={service.status} size="sm" />
          {noBackupWarning ? (
            <span
              className="inline-flex items-center gap-1 rounded-full border border-warn-border bg-warn-bg px-2 py-0.5 text-2xs font-medium text-warn-foreground"
              title="No backup is configured for this service — set one up on the Backups tab."
            >
              <ShieldAlert className="size-3" />
              No backup
            </span>
          ) : null}
        </div>
        <div className="flex gap-1">
          <ConfigureDialog appId={appId} service={service} />
          <Button
            variant="ghost"
            size="sm"
            disabled={restart.isPending}
            onClick={() =>
              restart.mutate(service.name, {
                onSuccess: () => toast.success(`Restarting ${service.name}`),
                onError: (e) => toast.error(e.message),
              })
            }
          >
            <RotateCw className="size-3.5" />
            Restart
          </Button>
        </div>
      </div>

      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 px-5 py-4 sm:grid-cols-4">
        <Metric label="CPU" value={live ? formatPercent(live.cpu_percent) : '—'} />
        <Metric label="Memory" value={live ? formatBytes(live.mem_bytes) : '—'} />
        <Metric label="Uptime" value={live ? formatDuration(live.uptime_seconds) : '—'} />
        <Metric label="Restarts" value={String(service.restart_count)} />
      </dl>

      <div className="flex flex-wrap gap-x-6 gap-y-1 border-t px-5 py-3 font-mono text-2xs text-muted-foreground">
        <span>port {service.exposed_port ?? service.internal_port ?? '—'}</span>
        <span>health {service.health_path ?? '—'}</span>
        {service.last_exit_code != null ? (
          <span className="text-err-foreground">exit {service.last_exit_code}</span>
        ) : null}
        {service.oom_killed_count > 0 ? (
          <span className="text-err-foreground" title="Killed for exceeding its memory limit">
            OOM-killed ×{service.oom_killed_count}
          </span>
        ) : null}
      </div>
    </Card>
  )
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
  const [open, setOpen] = useState(false)
  const [exposedPort, setExposedPort] = useState(service.exposed_port?.toString() ?? '')
  const [internalPort, setInternalPort] = useState(service.internal_port?.toString() ?? '')
  const [healthPath, setHealthPath] = useState(service.health_path ?? '')
  const update = useUpdateService(appId)

  const submit = () => {
    update.mutate(
      {
        name: service.name,
        input: {
          exposed_port: exposedPort ? Number(exposedPort) : undefined,
          internal_port: internalPort ? Number(internalPort) : undefined,
          health_path: healthPath || undefined,
        },
      },
      {
        onSuccess: () => {
          toast.success('Service updated')
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
          Configure
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Configure {service.name}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="grid gap-2">
            <Label htmlFor="exposed">Exposed port</Label>
            <Input
              id="exposed"
              inputMode="numeric"
              value={exposedPort}
              onChange={(e) => setExposedPort(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Host-published port — diagnostic only; HTTP services are reached over the internal
              network, not a host port.
            </p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="internal">Internal port</Label>
            <Input
              id="internal"
              inputMode="numeric"
              value={internalPort}
              onChange={(e) => setInternalPort(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              The port the container actually serves on. Saving re-routes traffic to it
              immediately — no restart needed.
            </p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="health">Health check path</Label>
            <Input
              id="health"
              placeholder="/"
              value={healthPath}
              onChange={(e) => setHealthPath(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="brand" disabled={update.isPending} onClick={submit}>
            Save changes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
