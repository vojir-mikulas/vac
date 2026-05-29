import { useMemo } from 'react'
import { Link } from '@tanstack/react-router'
import { Lock, ShieldAlert } from 'lucide-react'

import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ServicesTable } from '@/features/app-detail/services-table'
import { TrafficChart } from '@/features/app-detail/traffic-chart'
import { useAppStatsContext } from '@/features/app-detail/stats-context'
import { useServices } from '@/lib/api/services'
import { useDomains } from '@/lib/api/domains'
import { useDeployments } from '@/lib/api/deployments'
import { durationBetween, formatBytes, formatPercent, relativeTime, shortSha } from '@/lib/format'

export function OverviewTab({ appId }: { appId: string }) {
  const { data: services, isLoading } = useServices(appId)
  const { data: domains } = useDomains(appId)
  const { data: deployments } = useDeployments(appId)
  const stats = useAppStatsContext()

  const aggregate = useMemo(() => {
    let cpu = 0
    let mem = 0
    let running = 0
    for (const s of services ?? []) {
      const live = stats[s.name]
      if (live) {
        cpu += live.cpu_percent
        mem += live.mem_bytes
      }
      if (s.status === 'running') running++
    }
    return { cpu, mem, running, total: services?.length ?? 0 }
  }, [services, stats])

  const recentDeploys = deployments?.slice(0, 3) ?? []

  return (
    <div className="flex flex-col gap-6">
      <StatStrip>
        <StatTile
          label="Total CPU"
          value={formatPercent(aggregate.cpu)}
          sub="across services"
          accent
        />
        <StatTile label="Total memory" value={formatBytes(aggregate.mem)} sub="resident" />
        <StatTile
          label="Services"
          value={`${aggregate.running} / ${aggregate.total}`}
          sub="running"
        />
        <StatTile label="Domains" value={String(domains?.length ?? 0)} sub="configured" />
      </StatStrip>

      <TrafficChart appId={appId} />

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
          <SectionHeader>Services</SectionHeader>
          {isLoading ? (
            <Skeleton className="h-40 w-full rounded-xl" />
          ) : (
            <ServicesTable appId={appId} services={services ?? []} />
          )}
        </div>

        <div className="flex flex-col gap-6 lg:w-80 lg:shrink-0">
          <div>
            <SectionHeader>Domains</SectionHeader>
            <Card className="gap-0 p-0">
              {domains && domains.length > 0 ? (
                domains.map((d, i) => (
                  <div
                    key={d.id}
                    className={`flex items-center justify-between gap-2 px-4 py-3 ${i > 0 ? 'border-t' : ''}`}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      {d.cert_status === 'active' ? (
                        <Lock className="size-3.5 shrink-0 text-ok" />
                      ) : (
                        <ShieldAlert className="size-3.5 shrink-0 text-warn" />
                      )}
                      <span className="truncate font-mono text-xs">{d.hostname}</span>
                    </div>
                    <StatusPill
                      status={d.cert_status === 'active' ? 'success' : 'building'}
                      size="sm"
                    />
                  </div>
                ))
              ) : (
                <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                  No domains configured.
                </p>
              )}
            </Card>
          </div>

          <div>
            <SectionHeader
              action={
                <Link
                  to="/apps/$appId/deploys"
                  params={{ appId }}
                  className="text-2xs font-medium text-muted-foreground hover:text-foreground"
                >
                  View all
                </Link>
              }
            >
              Recent deploys
            </SectionHeader>
            <Card className="gap-0 p-0">
              {recentDeploys.length > 0 ? (
                recentDeploys.map((d, i) => (
                  <div
                    key={d.id}
                    className={`flex items-center justify-between gap-2 px-4 py-3 ${i > 0 ? 'border-t' : ''}`}
                  >
                    <div className="min-w-0">
                      <div className="truncate text-xs font-medium">
                        {d.commit_message ?? 'Deploy'}
                      </div>
                      <div className="font-mono text-2xs text-muted-foreground">
                        {shortSha(d.commit_sha)} · {relativeTime(d.triggered_at)} ·{' '}
                        {durationBetween(d.started_at, d.finished_at)}
                      </div>
                    </div>
                    <StatusPill status={d.status} size="sm" />
                  </div>
                ))
              ) : (
                <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                  No deployments yet.
                </p>
              )}
            </Card>
          </div>
        </div>
      </div>
    </div>
  )
}
