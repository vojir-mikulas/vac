import { useDeferredValue, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { m } from 'motion/react'
import { Link } from '@tanstack/react-router'
import {
  Blocks,
  Boxes,
  Download,
  GitBranch,
  Info,
  MoreHorizontal,
  Plus,
  Search,
} from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { BrandIcon, brandFor } from '@/components/common/brand-icon'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { Meter } from '@/components/common/meter'
import { EmptyState } from '@/components/common/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ListSkeleton } from '@/components/common/list-skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SwapFade } from '@/components/common/swap-fade'
import { cn } from '@/lib/utils'
import { listItem } from '@/lib/motion'
import { useApps } from '@/lib/api/apps'
import { useBoxBudget, useHostStats } from '@/lib/api/metrics'
import { formatBytes, formatPercent, relativeTime } from '@/lib/format'
import {
  appsBadgeVariant,
  countByFilter,
  matchesFilter,
  type AppFilter,
} from '@/features/apps/status-filter'
import { ImportAppDialog } from '@/features/apps/import-app-dialog'
import { OnboardingChecklist } from '@/features/onboarding/onboarding-checklist'
import type { App } from '@/types/api'

export function AppsDashboard() {
  const { t } = useTranslation('apps')
  const { data: apps, isLoading } = useApps()
  const { data: host } = useHostStats()
  const { data: budget } = useBoxBudget()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<AppFilter>('all')
  const [importOpen, setImportOpen] = useState(false)
  const deferredQuery = useDeferredValue(query)

  const list = useMemo(() => apps ?? [], [apps])
  const counts = useMemo(() => countByFilter(list), [list])

  const filtered = useMemo(() => {
    const q = deferredQuery.trim().toLowerCase()
    return list.filter(
      (app) =>
        matchesFilter(app, filter) &&
        (!q || app.name.toLowerCase().includes(q) || app.git_url.toLowerCase().includes(q)),
    )
  }, [list, deferredQuery, filter])

  return (
    <PageContainer>
      <PageHeader
        title={t('dashboard.title')}
        description={t('dashboard.summary', { count: counts.all, running: counts.running })}
        actions={
          <>
            <Button variant="outline" onClick={() => setImportOpen(true)}>
              <Download className="size-4" />
              {t('actions.import')}
            </Button>
            <Button variant="brand" asChild>
              <Link to="/apps/new">
                <Plus className="size-4" />
                {t('actions.newApp')}
              </Link>
            </Button>
          </>
        }
      />

      <ImportAppDialog open={importOpen} onOpenChange={setImportOpen} />

      <OnboardingChecklist />

      <div className="mb-6">
        <StatStrip>
          <StatTile
            label={t('dashboard.stats.runningApps')}
            value={String(counts.running)}
            sub={t('dashboard.stats.ofDeployed', { count: counts.all })}
            accent
          />
          <StatTile
            label={t('dashboard.stats.hostRam')}
            value={host ? formatBytes(host.mem_used_bytes) : '—'}
            sub={
              host
                ? t('dashboard.stats.ofSize', { size: formatBytes(host.mem_total_bytes) })
                : undefined
            }
          />
          <StatTile
            label={t('dashboard.stats.hostCpu')}
            value={host ? formatPercent(host.cpu_percent, 0) : '—'}
            sub={t('dashboard.stats.allCores')}
          />
          <StatTile
            label={t('dashboard.stats.disk')}
            value={host ? formatBytes(host.disk_used_bytes) : '—'}
            sub={
              host
                ? t('dashboard.stats.ofSize', { size: formatBytes(host.disk_total_bytes) })
                : undefined
            }
          />
        </StatStrip>
      </div>

      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="min-w-0 flex-1">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
            <div className="flex h-9 max-w-80 flex-1 basis-60 items-center gap-2 rounded-md border bg-background px-3">
              <Search className="size-3.5 text-muted-foreground" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t('dashboard.filterPlaceholder')}
                aria-label={t('dashboard.filterAria')}
                className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              />
            </div>
            <div className="flex flex-wrap gap-1.5">
              <FilterChip
                label={t('dashboard.filters.all')}
                count={counts.all}
                active={filter === 'all'}
                onClick={() => setFilter('all')}
              />
              <FilterChip
                label={t('dashboard.filters.running')}
                count={counts.running}
                active={filter === 'running'}
                onClick={() => setFilter('running')}
              />
              <FilterChip
                label={t('dashboard.filters.issues')}
                count={counts.issues}
                active={filter === 'issues'}
                onClick={() => setFilter('issues')}
              />
              <FilterChip
                label={t('dashboard.filters.stopped')}
                count={counts.stopped}
                active={filter === 'stopped'}
                onClick={() => setFilter('stopped')}
              />
            </div>
          </div>

          <SwapFade id={isLoading ? 'loading' : list.length === 0 ? 'empty' : 'table'}>
            {isLoading ? (
              <AppsTableSkeleton />
            ) : list.length === 0 ? (
              <EmptyState
                icon={Boxes}
                title={t('dashboard.empty.title')}
                description={t('dashboard.empty.description')}
                action={
                  <>
                    <Button variant="brand" asChild>
                      <Link to="/apps/new">
                        <Plus className="size-4" />
                        {t('actions.newApp')}
                      </Link>
                    </Button>
                    <Button variant="outline" onClick={() => setImportOpen(true)}>
                      <Download className="size-4" />
                      {t('actions.import')}
                    </Button>
                  </>
                }
              />
            ) : (
              <div className="overflow-hidden rounded-xl border">
                <Table>
                  <TableHeader>
                    <TableRow className="bg-surface-1 hover:bg-surface-1">
                      <TableHead className="text-2xs uppercase tracking-wider">
                        {t('dashboard.table.application')}
                      </TableHead>
                      <TableHead className="text-2xs uppercase tracking-wider">
                        {t('dashboard.table.status')}
                      </TableHead>
                      <TableHead className="text-2xs uppercase tracking-wider">
                        {t('dashboard.table.compose')}
                      </TableHead>
                      <TableHead className="text-2xs uppercase tracking-wider">
                        {t('dashboard.table.updated')}
                      </TableHead>
                      <TableHead className="w-10" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filtered.map((app, i) => (
                      <AppRow key={app.id} app={app} index={i} />
                    ))}
                    {filtered.length === 0 ? (
                      <TableRow>
                        <TableCell
                          colSpan={5}
                          className="py-10 text-center text-sm text-muted-foreground"
                        >
                          {t('dashboard.table.noMatch')}
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </TableBody>
                </Table>
              </div>
            )}
          </SwapFade>
        </div>

        <div className="lg:w-80 lg:shrink-0">
          <SectionHeader>{t('dashboard.budget.heading')}</SectionHeader>
          <Card className="gap-0 p-5">
            <div className="flex flex-col gap-3.5">
              <RunningAppsRow running={counts.running} all={counts.all} issues={counts.issues} />
              {host ? (
                <>
                  <BudgetRow
                    label={t('dashboard.budget.hostRam')}
                    current={host.mem_used_bytes}
                    total={host.mem_total_bytes}
                    bytes
                  />
                  <BudgetRow
                    label={t('dashboard.budget.disk')}
                    current={host.disk_used_bytes}
                    total={host.disk_total_bytes}
                    bytes
                  />
                </>
              ) : null}
              {budget && budget.total_ram_mb > 0 ? (
                <BudgetRow
                  label={t('dashboard.budget.allocatedRam')}
                  current={budget.allocated_mb}
                  total={budget.total_ram_mb}
                  unit="MiB"
                />
              ) : null}
            </div>
            {budget?.over_committed ? (
              <p className="mt-3 text-2xs text-err">{t('dashboard.budget.overCommitted')}</p>
            ) : budget && budget.apps_total > budget.apps_with_limit ? (
              <Badge variant="info" className="mt-3 text-2xs">
                <Info className="size-3" aria-hidden />
                {t('dashboard.budget.unbudgeted', {
                  count: budget.apps_total - budget.apps_with_limit,
                })}
              </Badge>
            ) : null}
            {host ? (
              <div className="mt-4 flex items-center justify-between border-t pt-3.5 text-xs text-muted-foreground">
                <span>{t('dashboard.budget.requestRate')}</span>
                <span className="font-mono text-foreground">
                  {t('dashboard.budget.reqPerSecond', { rate: host.request_rate.toFixed(1) })}
                </span>
              </div>
            ) : null}
          </Card>
        </div>
      </div>
    </PageContainer>
  )
}

// Animated table row: capped stagger entrance (via `index`) and `layout` so the
// rows above a filtered-out one glide up instead of snapping. Carries TableRow's
// own classes inline since motion needs to own the <tr> element directly.
function AppRow({ app, index }: { app: App; index: number }) {
  const { t } = useTranslation('apps')
  const isAddon = app.source === 'template'
  const brand = isAddon ? brandFor(app.template_icon) : null
  return (
    <m.tr
      layout
      custom={index}
      variants={listItem}
      initial="hidden"
      animate="visible"
      className="cursor-pointer border-b transition-colors hover:bg-muted/50 has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted"
    >
      <TableCell>
        <Link to="/apps/$appId" params={{ appId: app.id }} className="flex items-center gap-3">
          <span className="grid size-8 shrink-0 place-items-center rounded-md border bg-surface-2 font-mono text-sm font-semibold uppercase">
            {brand ? (
              <BrandIcon brand={app.template_icon} className="size-4" />
            ) : (
              app.name.slice(0, 1)
            )}
          </span>
          <span className="min-w-0">
            <span className="block truncate text-sm font-medium">{app.name}</span>
            {isAddon ? (
              <span className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
                <Blocks className="size-2.5" />
                <span className="truncate">
                  {t('dashboard.table.installedFrom', {
                    name: app.template_name ?? t('dashboard.table.addonFallback'),
                  })}
                </span>
              </span>
            ) : (
              <span className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
                <GitBranch className="size-2.5" />
                <span className="truncate">{app.git_url}</span>
                <span>:</span>
                <span>{app.git_branch}</span>
              </span>
            )}
          </span>
        </Link>
      </TableCell>
      <TableCell>
        <StatusPill status={app.status} />
      </TableCell>
      <TableCell className="font-mono text-xs text-muted-foreground">{app.compose_file}</TableCell>
      <TableCell className="text-xs text-muted-foreground">
        {relativeTime(app.updated_at)}
      </TableCell>
      <TableCell>
        <Button variant="ghost" size="icon-sm" className="text-muted-foreground">
          <MoreHorizontal className="size-4" />
        </Button>
      </TableCell>
    </m.tr>
  )
}

function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string
  count: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-md border px-3 text-xs font-medium transition-colors',
        active
          ? 'border-border-strong bg-surface-2 text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground',
      )}
    >
      {label}
      <span
        className={cn(
          'rounded px-1 font-mono text-2xs',
          active ? 'bg-background' : 'bg-surface-2 text-muted-foreground',
        )}
      >
        {count}
      </span>
    </button>
  )
}

// Occupancy ("apps up"), not utilisation — a health-toned count badge rather than
// a meter, so a fully-up box never reads red. See appsBadgeVariant for the mapping.
function RunningAppsRow({
  running,
  all,
  issues,
}: {
  running: number
  all: number
  issues: number
}) {
  const { t } = useTranslation('apps')
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-muted-foreground">{t('dashboard.stats.runningApps')}</span>
      <Badge
        variant={appsBadgeVariant({ running, all, issues })}
        className="font-mono tabular-nums"
      >
        {running}/{all}
      </Badge>
    </div>
  )
}

function BudgetRow({
  label,
  current,
  total,
  bytes,
  unit,
}: {
  label: string
  current: number
  total: number
  bytes?: boolean
  unit?: string
}) {
  const pct = total > 0 ? (current / total) * 100 : 0
  const display = bytes
    ? `${formatBytes(current)} / ${formatBytes(total)}`
    : `${current} / ${total}${unit ? ` ${unit}` : ''}`
  return (
    <div>
      <div className="mb-1.5 flex justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono tabular-nums">{display}</span>
      </div>
      <Meter pct={pct} className="h-1" tone="brand" label={label} />
    </div>
  )
}

function AppsTableSkeleton() {
  return <ListSkeleton header avatar rows={5} />
}
