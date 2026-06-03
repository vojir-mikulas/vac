import { Link } from '@tanstack/react-router'
import { Database, HardDrive, Info, ShieldCheck } from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { BrandIcon } from '@/components/common/brand-icon'
import { EmptyState } from '@/components/common/empty-state'
import { StatusPill } from '@/components/common/status-pill'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { StatStripSkeleton } from '@/components/common/stat-strip-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Badge } from '@/components/ui/badge'
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
  const { data: instance } = useInstanceInfo()

  return (
    <PageContainer>
      <PageHeader
        title="Database"
        description="Every database VAC manages on this box — disk usage, backups, and the app that owns each one."
      />
      <SwapFade
        id={instance == null ? 'loading' : instance.managed_services ? 'inventory' : 'control'}
      >
        {instance == null ? (
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
  const { data, isLoading } = useDatabaseInventory()
  const { data: host } = useHostStats()

  if (isLoading || !data) {
    return <DatabaseSkeleton />
  }

  const engines = data.engines
  const grandTotal = engines.reduce((acc, g) => acc + engineTotal(g).total, 0)
  const dbCount = engines.reduce((acc, g) => acc + g.databases.length, 0)

  return (
    <div className="flex flex-col gap-6">
      <StatStrip>
        <StatTile label="Engines" value={String(engines.length)} sub="live on this box" accent />
        <StatTile label="Databases" value={String(dbCount)} sub="managed by VAC" />
        <StatTile label="Managed disk" value={formatBytes(grandTotal)} sub="across all databases" />
        <StatTile
          label="Host disk"
          value={host ? formatBytes(host.disk_used_bytes) : '—'}
          sub={host ? `of ${formatBytes(host.disk_total_bytes)}` : undefined}
        />
      </StatStrip>

      {engines.length === 0 ? (
        <EmptyState
          icon={Database}
          title="No managed databases"
          description="Add a database from any app's Databases tab — VAC provisions it, injects the connection string, and schedules a nightly backup."
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
  const control = group.databases.find((d) => d.is_control_plane)
  const users = group.databases.filter((d) => !d.is_control_plane)
  const { total, unknown } = engineTotal(group)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span className="text-muted-foreground">
          Total disk: <span className="font-medium text-foreground">{formatBytes(total)}</span>
          {unknown > 0 ? <span className="text-muted-foreground"> · {unknown} unknown</span> : null}
        </span>
        {group.shared ? (
          <span className="text-2xs text-muted-foreground">
            Shared {group.engine} instance · ~{group.footprint_mb} MB
          </span>
        ) : null}
      </div>

      {control ? <VacCard entry={control} /> : null}

      {users.length > 0 ? (
        <Card className="gap-0 overflow-hidden p-0">
          <Table>
            <TableHeader>
              <TableRow className="bg-surface-1 hover:bg-surface-1">
                <TableHead className="text-2xs uppercase tracking-wider">Database</TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">App</TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">Size</TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">Status</TableHead>
                <TableHead className="text-2xs uppercase tracking-wider">Last backup</TableHead>
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
        <p className="text-sm text-muted-foreground">No databases on this engine yet.</p>
      )}

      {group.engine === 'mariadb' ? (
        <p className="text-2xs text-muted-foreground">
          Sizes are computed from <code className="font-mono">information_schema</code> and are
          approximate (InnoDB rounds allocation to extents).
        </p>
      ) : null}
    </div>
  )
}

function DatabaseRow({ entry: d }: { entry: DBInventoryEntry }) {
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
          <span className="text-xs text-muted-foreground">none</span>
        )}
      </TableCell>
    </TableRow>
  )
}

// VacCard pins the control-plane Postgres so the operator can never confuse VAC's
// own store with a user database.
function VacCard({ entry }: { entry: DBInventoryEntry }) {
  return (
    <Card className="gap-3 border-brand/30 bg-brand/[0.03] p-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <ShieldCheck className="size-4 text-brand" />
          <span className="font-mono text-sm font-semibold">{entry.db_name}</span>
          <Badge variant="info">VAC system database</Badge>
        </div>
        <span className="text-sm tabular-nums text-muted-foreground">
          {sizeLabel(entry.size_bytes)}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        VAC's own control-plane store. User Postgres databases live inside this same instance by
        default — don't touch this one.
      </p>
      <div className="flex items-center gap-2 border-t pt-3 text-2xs text-muted-foreground">
        <Info className="size-3" />
        {entry.last_backup
          ? `Last backup ${relativeTime(entry.last_backup.finished_at)}`
          : 'No backup configured for the control-plane database.'}
      </div>
    </Card>
  )
}

// ControlPlaneOnlyView is the degraded view when managed services are off: VAC
// still runs its own Postgres, so show that plus host disk rather than a blank page.
function ControlPlaneOnlyView() {
  const { data: host } = useHostStats()
  const diskPct = host ? (host.disk_used_bytes / host.disk_total_bytes) * 100 : 0

  return (
    <div className="flex flex-col gap-6">
      <StatStrip>
        <StatTile label="Engine" value="Postgres 16" sub="shared instance" accent />
        <StatTile
          label="Disk used"
          value={host ? formatBytes(host.disk_used_bytes) : '—'}
          sub={host ? `of ${formatBytes(host.disk_total_bytes)}` : undefined}
        />
        <StatTile label="Connections" value="≤ 50" sub="max_connections" />
      </StatStrip>

      <div className="flex flex-col gap-6 lg:flex-row">
        <Card className="min-w-0 flex-1 gap-3 p-5">
          <div className="flex items-center gap-2">
            <HardDrive className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Disk</h3>
          </div>
          <Meter pct={diskPct} className="h-1.5" tone="brand" />
          <p className="text-xs text-muted-foreground">
            {host
              ? `${formatBytes(host.disk_used_bytes)} of ${formatBytes(host.disk_total_bytes)} used on the host volume.`
              : 'Loading host volume usage…'}
          </p>
        </Card>

        <Card className="gap-3 p-5 lg:w-96 lg:shrink-0">
          <div className="flex items-center gap-2">
            <Database className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">About</h3>
          </div>
          <p className="text-xs text-muted-foreground">
            VAC stores its internal state in a lean shared Postgres tuned for small VPS hosts.
            Enable managed services to provision databases for your own apps and see them here.
          </p>
        </Card>
      </div>
    </div>
  )
}
