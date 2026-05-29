import { Database, HardDrive } from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { Card } from '@/components/ui/card'
import { Meter } from '@/components/common/meter'
import { useHostStats } from '@/lib/api/metrics'
import { formatBytes } from '@/lib/format'

export function DatabasePage() {
  const { data: host } = useHostStats()
  const diskPct = host ? (host.disk_used_bytes / host.disk_total_bytes) * 100 : 0

  return (
    <PageContainer>
      <PageHeader
        title="Database"
        description="VAC runs a single shared Postgres instance for its own state."
      />

      <div className="mb-6">
        <StatStrip>
          <StatTile label="Engine" value="Postgres 16" sub="shared instance" accent />
          <StatTile
            label="Disk used"
            value={host ? formatBytes(host.disk_used_bytes) : '—'}
            sub={host ? `of ${formatBytes(host.disk_total_bytes)}` : undefined}
          />
          <StatTile label="Connections" value="≤ 50" sub="max_connections" />
          <StatTile label="Backups" value="Nightly" sub="volume snapshot" />
        </StatStrip>
      </div>

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

        <Card className="lg:w-96 lg:shrink-0 gap-3 p-5">
          <div className="flex items-center gap-2">
            <Database className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">About</h3>
          </div>
          <p className="text-xs text-muted-foreground">
            VAC stores its internal state in a lean shared Postgres tuned for small VPS hosts.
            Managed databases for your own apps are planned for a future release.
          </p>
        </Card>
      </div>
    </PageContainer>
  )
}
