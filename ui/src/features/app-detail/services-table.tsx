import { useTranslation } from 'react-i18next'
import { RotateCw } from 'lucide-react'
import { toast } from 'sonner'

import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useRestartService } from '@/lib/api/services'
import { useAppStatsContext } from '@/features/app-detail/stats-context'
import { formatBytes, formatDuration, formatPercent } from '@/lib/format'
import type { Service } from '@/types/api'

export function ServicesTable({ appId, services }: { appId: string; services: Service[] }) {
  const { t } = useTranslation('app-detail')
  const stats = useAppStatsContext()
  const restart = useRestartService(appId)

  return (
    <div className="overflow-hidden rounded-xl border">
      <Table>
        <TableHeader>
          <TableRow className="bg-surface-1 hover:bg-surface-1">
            <TableHead className="text-2xs uppercase tracking-wider">
              {t('servicesTable.service')}
            </TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">
              {t('servicesTable.status')}
            </TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">
              {t('servicesTable.cpu')}
            </TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">
              {t('servicesTable.memory')}
            </TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">
              {t('servicesTable.uptime')}
            </TableHead>
            <TableHead className="text-right text-2xs uppercase tracking-wider">
              {t('servicesTable.actions')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {services.map((svc) => {
            const live = stats[svc.name]
            return (
              <TableRow key={svc.id}>
                <TableCell className="font-mono text-xs font-medium">
                  {svc.name}
                  {svc.restart_count > 0 ? (
                    <span className="ml-2 text-2xs text-warn-foreground">
                      {t('servicesTable.restartCount', { count: svc.restart_count })}
                    </span>
                  ) : null}
                </TableCell>
                <TableCell>
                  <StatusPill status={svc.status} size="sm" />
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">
                  {live ? formatPercent(live.cpu_percent) : '—'}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">
                  {live ? formatBytes(live.mem_bytes) : '—'}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                  {live ? formatDuration(live.uptime_seconds) : '—'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    // Restart only makes sense for a running container; a stopped
                    // service has nothing to restart (use the Services tab to start).
                    disabled={restart.isPending || svc.status !== 'running'}
                    title={
                      svc.status !== 'running' ? t('servicesTable.restartUnavailable') : undefined
                    }
                    onClick={() =>
                      restart.mutate(svc.name, {
                        onSuccess: () =>
                          toast.success(t('servicesTable.restarting', { service: svc.name })),
                        onError: (e) => toast.error(e.message),
                      })
                    }
                  >
                    <RotateCw className="size-3.5" />
                    {t('servicesTable.restart')}
                  </Button>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
