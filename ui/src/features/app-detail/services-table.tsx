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
  const stats = useAppStatsContext()
  const restart = useRestartService(appId)

  return (
    <div className="overflow-hidden rounded-xl border">
      <Table>
        <TableHeader>
          <TableRow className="bg-surface-1 hover:bg-surface-1">
            <TableHead className="text-2xs uppercase tracking-wider">Service</TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">Status</TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">CPU</TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">Memory</TableHead>
            <TableHead className="text-2xs uppercase tracking-wider">Uptime</TableHead>
            <TableHead className="text-right text-2xs uppercase tracking-wider">Actions</TableHead>
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
                      ↻ {svc.restart_count}
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
                    disabled={restart.isPending}
                    onClick={() =>
                      restart.mutate(svc.name, {
                        onSuccess: () => toast.success(`Restarting ${svc.name}`),
                        onError: (e) => toast.error(e.message),
                      })
                    }
                  >
                    <RotateCw className="size-3.5" />
                    Restart
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
