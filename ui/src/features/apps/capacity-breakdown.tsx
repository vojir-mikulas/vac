import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Meter } from '@/components/common/meter'
import { useBoxCapacity } from '@/lib/api/metrics'
import { formatBytes } from '@/lib/format'
import type { CapacityApp } from '@/types/api'

const MIB = 1024 * 1024

// CapacityBreakdown is the per-app RAM detail behind the budget panel: committed
// cap vs live actual usage per app. The underlying poll only runs while the
// dialog is open (useBoxCapacity gates on `open`), so it costs nothing at idle.
export function CapacityBreakdown() {
  const { t } = useTranslation('apps')
  const [open, setOpen] = useState(false)
  const { data, isLoading } = useBoxCapacity(open)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="mt-3 text-2xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
      >
        {t('dashboard.capacity.view')}
      </button>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('dashboard.capacity.title')}</DialogTitle>
          <DialogDescription>{t('dashboard.capacity.description')}</DialogDescription>
        </DialogHeader>
        {isLoading && !data ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {t('dashboard.capacity.loading')}
          </p>
        ) : !data || data.apps.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {t('dashboard.capacity.empty')}
          </p>
        ) : (
          <ul className="flex max-h-[60vh] flex-col gap-3.5 overflow-y-auto pr-1">
            {data.apps.map((app) => (
              <CapacityRow key={app.slug} app={app} />
            ))}
          </ul>
        )}
      </DialogContent>
    </Dialog>
  )
}

function CapacityRow({ app }: { app: CapacityApp }) {
  const { t } = useTranslation('apps')
  const capBytes = app.mem_limit_mb ? app.mem_limit_mb * MIB : 0
  const pct = capBytes > 0 ? (app.actual_mem_bytes / capBytes) * 100 : 0
  return (
    <li>
      <div className="mb-1.5 flex items-center justify-between gap-2 text-xs">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate font-medium">{app.name}</span>
          {!app.running ? (
            <Badge variant="outline" className="text-2xs">
              {t('dashboard.capacity.stopped')}
            </Badge>
          ) : app.mem_limit_mb == null ? (
            <Badge variant="info" className="text-2xs">
              {t('dashboard.capacity.unbudgeted')}
            </Badge>
          ) : null}
        </span>
        <span className="shrink-0 font-mono tabular-nums text-muted-foreground">
          {app.mem_limit_mb
            ? `${formatBytes(app.actual_mem_bytes)} / ${app.mem_limit_mb} MiB`
            : formatBytes(app.actual_mem_bytes)}
        </span>
      </div>
      {app.mem_limit_mb ? <Meter pct={pct} className="h-1" tone="brand" label={app.name} /> : null}
    </li>
  )
}
