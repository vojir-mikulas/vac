import { useDeferredValue, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Boxes, GitBranch, MoreHorizontal, Plus, Search } from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { Meter } from '@/components/common/meter'
import { EmptyState } from '@/components/common/empty-state'
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
import { cn } from '@/lib/utils'
import { useApps } from '@/lib/api/apps'
import { useHostStats } from '@/lib/api/metrics'
import { formatBytes, formatPercent, relativeTime } from '@/lib/format'
import { countByFilter, matchesFilter, type AppFilter } from '@/features/apps/status-filter'
import type { App } from '@/types/api'

export function AppsDashboard() {
  const { data: apps, isLoading } = useApps()
  const { data: host } = useHostStats()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<AppFilter>('all')
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
        title="Apps"
        description={`${counts.all} application${counts.all === 1 ? '' : 's'} · ${counts.running} running`}
        actions={
          <Button variant="brand" asChild>
            <Link to="/apps/new">
              <Plus className="size-4" />
              New App
            </Link>
          </Button>
        }
      />

      <div className="mb-6">
        <StatStrip>
          <StatTile
            label="Running apps"
            value={String(counts.running)}
            sub={`of ${counts.all} deployed`}
            accent
          />
          <StatTile
            label="Host RAM"
            value={host ? formatBytes(host.mem_used_bytes) : '—'}
            sub={host ? `of ${formatBytes(host.mem_total_bytes)}` : undefined}
          />
          <StatTile
            label="Host CPU"
            value={host ? formatPercent(host.cpu_percent, 0) : '—'}
            sub="across all cores"
          />
          <StatTile
            label="Disk"
            value={host ? formatBytes(host.disk_used_bytes) : '—'}
            sub={host ? `of ${formatBytes(host.disk_total_bytes)}` : undefined}
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
                placeholder="Filter apps…"
                className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              />
            </div>
            <div className="flex flex-wrap gap-1.5">
              <FilterChip
                label="All"
                count={counts.all}
                active={filter === 'all'}
                onClick={() => setFilter('all')}
              />
              <FilterChip
                label="Running"
                count={counts.running}
                active={filter === 'running'}
                onClick={() => setFilter('running')}
              />
              <FilterChip
                label="Issues"
                count={counts.issues}
                active={filter === 'issues'}
                onClick={() => setFilter('issues')}
              />
              <FilterChip
                label="Stopped"
                count={counts.stopped}
                active={filter === 'stopped'}
                onClick={() => setFilter('stopped')}
              />
            </div>
          </div>

          {isLoading ? (
            <AppsTableSkeleton />
          ) : list.length === 0 ? (
            <EmptyState
              icon={Boxes}
              title="No apps yet"
              description="Connect a repository to deploy your first app."
              action={
                <Button variant="brand" asChild>
                  <Link to="/apps/new">
                    <Plus className="size-4" />
                    New App
                  </Link>
                </Button>
              }
            />
          ) : (
            <div className="overflow-hidden rounded-xl border">
              <Table>
                <TableHeader>
                  <TableRow className="bg-surface-1 hover:bg-surface-1">
                    <TableHead className="text-2xs uppercase tracking-wider">Application</TableHead>
                    <TableHead className="text-2xs uppercase tracking-wider">Status</TableHead>
                    <TableHead className="text-2xs uppercase tracking-wider">Compose</TableHead>
                    <TableHead className="text-2xs uppercase tracking-wider">Updated</TableHead>
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((app) => (
                    <AppRow key={app.id} app={app} />
                  ))}
                  {filtered.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-10 text-center text-sm text-muted-foreground"
                      >
                        No apps match the current filter.
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>
          )}
        </div>

        <div className="lg:w-80 lg:shrink-0">
          <SectionHeader>Container budget</SectionHeader>
          <Card className="gap-0 p-5">
            <div className="flex flex-col gap-3.5">
              <BudgetRow
                label="Running apps"
                current={counts.running}
                total={counts.all || 1}
                unit=""
              />
              {host ? (
                <>
                  <BudgetRow
                    label="Host RAM"
                    current={host.mem_used_bytes}
                    total={host.mem_total_bytes}
                    bytes
                  />
                  <BudgetRow
                    label="Disk"
                    current={host.disk_used_bytes}
                    total={host.disk_total_bytes}
                    bytes
                  />
                </>
              ) : null}
            </div>
            {host ? (
              <div className="mt-4 flex items-center justify-between border-t pt-3.5 text-xs text-muted-foreground">
                <span>Request rate</span>
                <span className="font-mono text-foreground">
                  {host.request_rate.toFixed(1)} req/s
                </span>
              </div>
            ) : null}
          </Card>
        </div>
      </div>
    </PageContainer>
  )
}

function AppRow({ app }: { app: App }) {
  return (
    <TableRow className="cursor-pointer">
      <TableCell>
        <Link to="/apps/$appId" params={{ appId: app.id }} className="flex items-center gap-3">
          <span className="grid size-8 shrink-0 place-items-center rounded-md border bg-surface-2 font-mono text-sm font-semibold uppercase">
            {app.name.slice(0, 1)}
          </span>
          <span className="min-w-0">
            <span className="block truncate text-sm font-medium">{app.name}</span>
            <span className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
              <GitBranch className="size-2.5" />
              <span className="truncate">{app.git_url}</span>
              <span>:</span>
              <span>{app.git_branch}</span>
            </span>
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
    </TableRow>
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
        'inline-flex h-9 items-center gap-1.5 rounded-md border px-3 text-xs font-medium transition-colors',
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
      <Meter pct={pct} className="h-1" tone="brand" />
    </div>
  )
}

function AppsTableSkeleton() {
  return (
    <div className="flex flex-col gap-2">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-14 w-full rounded-xl" />
      ))}
    </div>
  )
}
