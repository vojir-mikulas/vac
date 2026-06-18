import { Link } from '@tanstack/react-router'
import { Blocks, Database, HardDrive, Info, ShieldCheck } from 'lucide-react'
import { Trans, useTranslation } from 'react-i18next'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { BrandIcon } from '@/components/common/brand-icon'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { StatusPill } from '@/components/common/status-pill'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { StatStripSkeleton } from '@/components/common/stat-strip-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Meter } from '@/components/common/meter'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useDatabaseInventory } from '@/lib/api/db-inventory'
import { useInstanceInfo } from '@/lib/api/instance'
import { useHostStats } from '@/lib/api/metrics'
import { formatBytes, relativeTime } from '@/lib/format'
import type { DBEngineGroup, DBInventoryEntry } from '@/types/api'

export function DatabasePage() {
  const { t } = useTranslation('database')
  const { data: instance, isError, refetch } = useInstanceInfo()

  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />
      <SwapFade
        id={
          isError
            ? 'error'
            : instance == null
              ? 'loading'
              : instance.managed_services
                ? 'inventory'
                : 'control'
        }
      >
        {isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : instance == null ? (
          <DatabaseSkeleton />
        ) : instance.managed_services ? (
          <InventoryView />
        ) : (
          <ControlPlaneOnlyView />
        )}
      </SwapFade>
    </PageContainer>
  )
}

// Mirrors the loaded shape — a 4-tile stat strip over a list — so the gate doesn't
// resize when real data lands.
function DatabaseSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      <StatStripSkeleton />
      <ListSkeleton rows={4} />
    </div>
  )
}

// 'ready' renders green; everything else passes through to StatusPill's mapping.
function pillStatus(status: string): string {
  return status === 'ready' ? 'success' : status
}

function sizeLabel(n: number | null | undefined): string {
  return n == null ? '—' : formatBytes(n)
}

function engineTotal(g: DBEngineGroup): { total: number; unknown: number } {
  let total = 0
  let unknown = 0
  for (const d of g.databases) {
    if (d.size_bytes == null) unknown++
    else total += d.size_bytes
  }
  return { total, unknown }
}

function InventoryView() {
  const { t } = useTranslation('database')
  const { data, isLoading, isError, refetch } = useDatabaseInventory()
  const { data: host } = useHostStats()

  if (isError) {
    return <ErrorState onRetry={() => refetch()} />
  }
  if (isLoading || !data) {
    return <DatabaseSkeleton />
  }

  const engines = data.engines
  const grandTotal = engines.reduce((acc, g) => acc + engineTotal(g).total, 0)
  const dbCount = engines.reduce((acc, g) => acc + g.databases.length, 0)

  return (
    <div className="flex flex-col gap-6">
      <StatStrip>
        <StatTile
          label={t('stats.engines')}
          value={String(engines.length)}
          sub={t('stats.enginesSub')}
          accent
        />
        <StatTile
          label={t('stats.databases')}
          value={String(dbCount)}
          sub={t('stats.databasesSub')}
        />
        <StatTile
          label={t('stats.managedDisk')}
          value={formatBytes(grandTotal)}
          sub={t('stats.managedDiskSub')}
        />
        <StatTile
          label={t('stats.hostDisk')}
          value={host ? formatBytes(host.disk_used_bytes) : '—'}
          sub={
            host ? t('stats.hostDiskSub', { size: formatBytes(host.disk_total_bytes) }) : undefined
          }
        />
      </StatStrip>

      {engines.length === 0 ? (
        <EmptyState
          icon={Database}
          title={t('empty.title')}
          description={t('empty.description')}
          action={
            <Button variant="brand" asChild>
              <Link to="/addons">
                <Blocks className="size-4" />
                {t('empty.action')}
              </Link>
            </Button>
          }
        />
      ) : (
        <Tabs defaultValue={engines[0]?.engine}>
          <TabsList>
            {engines.map((g) => (
              <TabsTrigger key={g.engine} value={g.engine} className="gap-1.5 px-3 capitalize">
                <BrandIcon brand={g.engine} className="size-3.5" />
                {g.engine}
                <span className="text-2xs text-muted-foreground">{g.databases.length}</span>
              </TabsTrigger>
            ))}
          </TabsList>
          {engines.map((g) => (
            <TabsContent key={g.engine} value={g.engine} className="mt-4">
              <EngineTab group={g} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  )
}

function EngineTab({ group }: { group: DBEngineGroup }) {
  const { t } = useTranslation('database')
  const control = group.databases.find((d) => d.is_control_plane)
  const users = group.databases.filter((d) => !d.is_control_plane)
  const { total, unknown } = engineTotal(group)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span className="text-muted-foreground">
          {t('engine.totalDisk')}{' '}
          <span className="font-medium text-foreground">{formatBytes(total)}</span>
          {unknown > 0 ? (
            <span className="text-muted-foreground">{t('engine.unknown', { count: unknown })}</span>
          ) : null}
        </span>
        {group.shared ? (
          <span className="text-2xs text-muted-foreground">
            {t('engine.sharedInstance', { engine: group.engine, footprint: group.footprint_mb })}
          </span>
        ) : null}
      </div>

      {control ? <VacCard entry={control} /> : null}

      {users.length > 0 ? (
        <Card className="gap-0 overflow-hidden p-0">
          <Table>
            <TableHeader>
              <TableRow className="bg-surface-1 hover:bg-surface-1">
                <TableHead className="text-2xs uppercase tracking-wider">
                  {t('table.database')}
                </TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">
                  {t('table.app')}
                </TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">
                  {t('table.size')}
                </TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">
                  {t('table.status')}
                </TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">
                  {t('table.lastBackup')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((d) => (
                <DatabaseRow key={d.id ?? d.db_name} entry={d} />
              ))}
            </TableBody>
          </Table>
        </Card>
      ) : (
        <p className="text-sm text-muted-foreground">{t('engine.noDatabases')}</p>
      )}

      {group.engine === 'mariadb' ? (
        <p className="text-2xs text-muted-foreground">
          <Trans t={t} i18nKey="engine.mariadbNote" components={[<code className="font-mono" />]} />
        </p>
      ) : null}
    </div>
  )
}

function DatabaseRow({ entry: d }: { entry: DBInventoryEntry }) {
  const { t } = useTranslation('database')
  return (
    <TableRow>
      <TableCell className="font-mono text-xs">{d.db_name}</TableCell>
      <TableCell>
        {d.app_id ? (
          <Link
            to="/apps/$appId"
            params={{ appId: d.app_id }}
            className="text-brand hover:underline"
          >
            {d.app_name || d.app_slug || d.app_id}
          </Link>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell className="tabular-nums">{sizeLabel(d.size_bytes)}</TableCell>
      <TableCell>
        <StatusPill status={pillStatus(d.status)} size="sm" />
      </TableCell>
      <TableCell>
        {d.last_backup ? (
          <span className="flex items-center gap-2 text-xs">
            <StatusPill status={d.last_backup.status} size="sm" />
            <span className="text-muted-foreground">
              {relativeTime(d.last_backup.finished_at)}
              {d.last_backup.size_bytes != null
                ? ` · ${formatBytes(d.last_backup.size_bytes)}`
                : ''}
            </span>
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">{t('table.noBackup')}</span>
        )}
      </TableCell>
    </TableRow>
  )
}

// VacCard pins the control-plane Postgres so the operator can never confuse VAC's
// own store with a user database.
function VacCard({ entry }: { entry: DBInventoryEntry }) {
  const { t } = useTranslation('database')
  return (
    <Card className="gap-3 border-brand/30 bg-brand/[0.03] p-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <ShieldCheck className="size-4 text-brand" />
          <span className="font-mono text-sm font-semibold">{entry.db_name}</span>
          <Badge variant="info">{t('vacCard.systemBadge')}</Badge>
        </div>
        <span className="text-sm tabular-nums text-muted-foreground">
          {sizeLabel(entry.size_bytes)}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">{t('vacCard.description')}</p>
      <div className="flex items-center gap-2 border-t pt-3 text-2xs text-muted-foreground">
        <Info className="size-3" />
        {entry.last_backup
          ? t('vacCard.lastBackup', { time: relativeTime(entry.last_backup.finished_at) })
          : t('vacCard.noBackup')}
      </div>
    </Card>
  )
}

// ControlPlaneOnlyView is the degraded view when managed services are off: VAC
// still runs its own Postgres, so show that plus host disk rather than a blank page.
function ControlPlaneOnlyView() {
  const { t } = useTranslation('database')
  const { data: host } = useHostStats()
  const diskPct = host ? (host.disk_used_bytes / host.disk_total_bytes) * 100 : 0

  return (
    <div className="flex flex-col gap-6">
      <StatStrip>
        <StatTile
          label={t('controlPlane.engine')}
          value="Postgres 16"
          sub={t('controlPlane.engineSub')}
          accent
        />
        <StatTile
          label={t('controlPlane.diskUsed')}
          value={host ? formatBytes(host.disk_used_bytes) : '—'}
          sub={
            host
              ? t('controlPlane.diskUsedSub', { size: formatBytes(host.disk_total_bytes) })
              : undefined
          }
        />
        <StatTile label={t('controlPlane.connections')} value="≤ 50" sub="max_connections" />
      </StatStrip>

      <div className="flex flex-col gap-6 lg:flex-row">
        <Card className="min-w-0 flex-1 gap-3 p-5">
          <div className="flex items-center gap-2">
            <HardDrive className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">{t('controlPlane.disk')}</h3>
          </div>
          <Meter pct={diskPct} className="h-1.5" tone="brand" />
          <p className="text-xs text-muted-foreground">
            {host
              ? t('controlPlane.diskUsage', {
                  used: formatBytes(host.disk_used_bytes),
                  total: formatBytes(host.disk_total_bytes),
                })
              : t('controlPlane.diskLoading')}
          </p>
        </Card>

        <Card className="gap-3 p-5 lg:w-96 lg:shrink-0">
          <div className="flex items-center gap-2">
            <Database className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">{t('controlPlane.about')}</h3>
          </div>
          <p className="text-xs text-muted-foreground">{t('controlPlane.aboutDescription')}</p>
        </Card>
      </div>
    </div>
  )
}
