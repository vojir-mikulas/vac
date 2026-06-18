import { useTranslation } from 'react-i18next'
import { Link } from '@tanstack/react-router'
import { Boxes, Download, Pencil, Play } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { backupsApi, useFleetBackups, useRunFleetBackup } from '@/lib/api/backups'
import { formatBackupSize, scheduleSummary, type ScheduleLabels } from '@/lib/backups'
import { formatBytes } from '@/lib/format'
import type { FleetBackupConfig } from '@/types/api'

type BackupsT = ReturnType<typeof useTranslation<'backups'>>['t']

// Day-of-week → its catalog key in the `backups` namespace, as a literal tuple
// so t() stays type-safe (a `days.${number}` template would be too wide).
const DAY_KEYS = ['days.0', 'days.1', 'days.2', 'days.3', 'days.4', 'days.5', 'days.6'] as const

function scheduleLabels(t: BackupsT): ScheduleLabels {
  return {
    weekly: (v) => t('weeklySummary', v),
    daily: (v) => t('dailySummary', v),
    dayName: (i) => t(DAY_KEYS[i] ?? DAY_KEYS[0]),
  }
}

export function BackupsPage() {
  const { t } = useTranslation('backups')
  const { data, isLoading, isError, refetch } = useFleetBackups()
  const configs = data?.configs ?? []
  const uncovered = data?.uncovered ?? []

  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />

      <div className="mb-6">
        <StatStrip>
          <StatTile
            label={t('stats.configs')}
            value={String(data?.summary.configs ?? 0)}
            sub={t('stats.configsSub')}
            accent
          />
          <StatTile
            label={t('stats.failed')}
            value={String(data?.summary.failed_last_7d ?? 0)}
            sub={t('stats.failedSub')}
            tone={data && data.summary.failed_last_7d > 0 ? 'err' : undefined}
          />
          <StatTile
            label={t('stats.uncovered')}
            value={String(data?.summary.uncovered_services ?? 0)}
            sub={t('stats.uncoveredSub')}
            tone={data && data.summary.uncovered_services > 0 ? 'warn' : undefined}
          />
          <StatTile
            label={t('stats.localDisk')}
            value={formatBytes(data?.summary.local_bytes ?? 0)}
            sub={t('stats.localDiskSub')}
          />
        </StatStrip>
      </div>

      <SectionHeader>{t('table.title')}</SectionHeader>
      <SwapFade
        id={isLoading ? 'loading' : isError ? 'error' : configs.length === 0 ? 'empty' : 'jobs'}
      >
        {isLoading ? (
          <ListSkeleton header rows={4} />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : configs.length === 0 ? (
          <EmptyState
            title={t('empty.title')}
            description={t('empty.description')}
            action={
              <Button variant="brand" asChild>
                <Link to="/apps">
                  <Boxes className="size-4" />
                  {t('empty.action')}
                </Link>
              </Button>
            }
          />
        ) : (
          <Card className="gap-0 overflow-hidden p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('table.app')}</TableHead>
                  <TableHead>{t('table.service')}</TableHead>
                  <TableHead>{t('table.schedule')}</TableHead>
                  <TableHead>{t('table.destination')}</TableHead>
                  <TableHead>{t('table.lastRun')}</TableHead>
                  <TableHead className="text-right">{t('table.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {configs.map((c) => (
                  <JobRow key={c.id} config={c} t={t} />
                ))}
              </TableBody>
            </Table>
          </Card>
        )}
      </SwapFade>

      {uncovered.length > 0 ? (
        <div className="mt-8">
          <SectionHeader>{t('uncovered.title')}</SectionHeader>
          <p className="-mt-2 mb-3 text-sm text-muted-foreground">{t('uncovered.description')}</p>
          <Card className="gap-0 overflow-hidden p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('table.app')}</TableHead>
                  <TableHead>{t('table.service')}</TableHead>
                  <TableHead className="text-right">{t('table.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {uncovered.map((u) => (
                  <TableRow key={`${u.app_id}/${u.service_name}`}>
                    <TableCell className="font-mono text-xs font-medium">{u.app_name}</TableCell>
                    <TableCell className="font-mono text-xs">{u.service_name}</TableCell>
                    <TableCell className="text-right">
                      <Button asChild variant="outline" size="sm">
                        <Link to="/apps/$appId/backups" params={{ appId: u.app_id }}>
                          {t('uncovered.configure')}
                        </Link>
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
        </div>
      ) : null}
    </PageContainer>
  )
}

function JobRow({ config, t }: { config: FleetBackupConfig; t: BackupsT }) {
  const run = useRunFleetBackup()
  const lastRun = config.last_run
  const canDownload = lastRun?.status === 'success'

  return (
    <TableRow>
      <TableCell>
        <Link
          to="/apps/$appId/backups"
          params={{ appId: config.app_id }}
          className="font-mono text-xs font-medium hover:underline"
        >
          {config.app_name}
        </Link>
      </TableCell>
      <TableCell>
        <span className="font-mono text-xs">{config.service_name}</span>
        {!config.enabled ? (
          <span className="ml-2 text-2xs uppercase tracking-wider text-muted-foreground">
            {t('paused')}
          </span>
        ) : null}
      </TableCell>
      <TableCell className="text-xs">{scheduleSummary(config, scheduleLabels(t))}</TableCell>
      <TableCell className="text-xs">
        {config.destination === 's3' ? t('destinationS3') : t('destinationLocal')}
      </TableCell>
      <TableCell>
        {lastRun ? (
          <div className="flex items-center gap-2">
            <StatusPill status={lastRun.status} size="sm" />
            <span className="text-2xs text-muted-foreground">
              {lastRun.finished_at
                ? `${new Date(lastRun.finished_at).toLocaleString()} · ${formatBackupSize(lastRun.size_bytes)}`
                : ''}
            </span>
          </div>
        ) : (
          <span className="text-xs text-muted-foreground">{t('lastRunNever')}</span>
        )}
      </TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={run.isPending}
            onClick={() =>
              run.mutate(
                { appId: config.app_id, cid: config.id },
                {
                  onSuccess: () => toast.success(t('backupStarted')),
                  onError: (e) => toast.error(e.message),
                },
              )
            }
          >
            <Play className="size-3.5" />
            {t('backupNow')}
          </Button>
          {canDownload ? (
            <a
              className="inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline"
              href={backupsApi.downloadUrl(config.app_id, lastRun.id)}
              download
            >
              <Download className="size-3.5" />
              {t('download')}
            </a>
          ) : null}
          <Button asChild variant="ghost" size="sm" aria-label={t('edit')}>
            <Link to="/apps/$appId/backups" params={{ appId: config.app_id }}>
              <Pencil className="size-3.5" />
            </Link>
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}
