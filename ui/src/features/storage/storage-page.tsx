import { Link } from '@tanstack/react-router'
import { HardDrive, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { SectionHeader } from '@/components/common/section-header'
import { Meter } from '@/components/common/meter'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  useInstanceStorage,
  usePruneDisk,
  type AppStorage,
  type DiskUsage,
  type DiskUsageEntry,
} from '@/lib/api/instance'
import { formatBytes } from '@/lib/format'
import { HostDiskChart, HOST_COLORS } from './host-disk-chart'

export function StoragePage() {
  const { t } = useTranslation('storage')
  const { data, isLoading, isError, refetch } = useInstanceStorage()

  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />
      {isLoading ? (
        <StorageSkeleton />
      ) : isError || !data ? (
        <ErrorState onRetry={() => refetch()} />
      ) : (
        <div className="flex flex-col gap-8">
          <HostDisk host={data.host} />
          <AppsByStorage apps={data.apps} />
        </div>
      )}
    </PageContainer>
  )
}

// Host docker breakdown + reclaim, mirroring Settings → Maintenance so storage
// management has one home. Reclaim uses the same prune mutation/endpoint.
function HostDisk({ host }: { host: DiskUsage }) {
  const { t } = useTranslation('storage')
  const prune = usePruneDisk()

  const reclaimable = host.images.reclaimable_bytes + host.build_cache.reclaimable_bytes
  const nothingToReclaim = reclaimable <= 0

  const reclaim = () =>
    prune.mutate(undefined, {
      onSuccess: (r) =>
        toast.success(
          r.total_reclaimed_bytes > 0
            ? t('host.reclaimed', { size: formatBytes(r.total_reclaimed_bytes) })
            : t('host.reclaimedNothing'),
        ),
      onError: (e) => toast.error(e.message),
    })

  return (
    <section>
      <SectionHeader>{t('host.heading')}</SectionHeader>
      <Card className="gap-5 p-5">
        <div className="flex flex-col items-center gap-6 sm:flex-row sm:items-center sm:gap-8">
          <HostDiskChart host={host} className="w-40 shrink-0" />
          <div className="flex w-full flex-1 flex-col gap-2">
            <UsageRow label={t('host.images')} entry={host.images} color={HOST_COLORS.images} />
            <UsageRow
              label={t('host.containers')}
              entry={host.containers}
              color={HOST_COLORS.containers}
            />
            <UsageRow label={t('host.volumes')} entry={host.volumes} color={HOST_COLORS.volumes} />
            <UsageRow
              label={t('host.buildCache')}
              entry={host.build_cache}
              color={HOST_COLORS.buildCache}
            />
          </div>
        </div>
        <div className="flex items-center justify-between gap-4">
          <p className="text-xs text-muted-foreground">{t('host.note')}</p>
          <Button
            variant="outline"
            size="sm"
            disabled={prune.isPending || nothingToReclaim}
            onClick={reclaim}
          >
            {prune.isPending ? t('host.reclaiming') : t('host.reclaim')}
          </Button>
        </div>
      </Card>
    </section>
  )
}

function UsageRow({
  label,
  entry,
  color,
}: {
  label: string
  entry: DiskUsageEntry
  color: string
}) {
  const { t } = useTranslation('storage')
  return (
    <div className="flex items-baseline justify-between gap-4 border-b pb-2 last:border-b-0 last:pb-0">
      <span className="flex items-center gap-2 text-sm font-medium">
        <span
          className="size-2.5 shrink-0 rounded-[2px]"
          style={{ background: color }}
          aria-hidden
        />
        {label}
      </span>
      <div className="flex items-baseline gap-3 font-mono text-xs">
        <span className="tabular-nums">{formatBytes(entry.size_bytes)}</span>
        {entry.reclaimable_bytes > 0 ? (
          <span className="text-muted-foreground">
            {t('host.reclaimable', { size: formatBytes(entry.reclaimable_bytes) })}
          </span>
        ) : null}
      </div>
    </div>
  )
}

// Per-app volume totals, heaviest first (the server sorts). The "+N not measured"
// hint keeps the total honest when bind-mount scanning is off.
function AppsByStorage({ apps }: { apps: AppStorage[] }) {
  const { t } = useTranslation('storage')

  if (apps.length === 0) {
    return (
      <section>
        <SectionHeader>{t('apps.heading')}</SectionHeader>
        <EmptyState
          icon={HardDrive}
          title={t('empty.title')}
          description={t('empty.description')}
          action={
            <Button variant="brand" asChild>
              <Link to="/apps/new">
                <Plus className="size-4" />
                {t('empty.action')}
              </Link>
            </Button>
          }
        />
      </section>
    )
  }

  return (
    <section>
      <SectionHeader>{t('apps.heading')}</SectionHeader>
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('apps.app')}</TableHead>
              <TableHead className="text-right">{t('apps.volumes')}</TableHead>
              <TableHead className="text-right">{t('apps.used')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {apps.map((app) => (
              <AppRow key={app.id} app={app} />
            ))}
          </TableBody>
        </Table>
      </Card>
    </section>
  )
}

function AppRow({ app }: { app: AppStorage }) {
  const { t } = useTranslation('storage')
  const pct = app.limit_bytes && app.limit_bytes > 0 ? (app.used_bytes / app.limit_bytes) * 100 : 0

  return (
    <TableRow>
      <TableCell>
        <Link to="/apps/$appId" params={{ appId: app.id }} className="font-medium hover:underline">
          {app.name}
        </Link>
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">
        {app.volume_count}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex flex-col items-end gap-1">
          <span className="font-mono text-xs tabular-nums">
            {app.limit_bytes && app.limit_bytes > 0
              ? `${formatBytes(app.used_bytes)} / ${formatBytes(app.limit_bytes)}`
              : formatBytes(app.used_bytes)}
            {app.unmeasured_count > 0 ? (
              <span className="ml-1.5 text-muted-foreground">
                {t('apps.unmeasured', { n: app.unmeasured_count })}
              </span>
            ) : null}
          </span>
          {app.limit_bytes && app.limit_bytes > 0 ? (
            <Meter pct={pct} className="h-1 w-24" tone="brand" label={t('apps.used')} />
          ) : null}
        </div>
      </TableCell>
    </TableRow>
  )
}

function StorageSkeleton() {
  return (
    <div className="flex flex-col gap-8">
      <Skeleton className="h-44 w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}
