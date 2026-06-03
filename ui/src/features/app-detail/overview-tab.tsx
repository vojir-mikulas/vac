import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from '@tanstack/react-router'
import { ExternalLink, Lock, ShieldAlert } from 'lucide-react'

import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Card } from '@/components/ui/card'
import { OverviewPanel } from '@/features/app-detail/overview-panel'
import { ServicesTable } from '@/features/app-detail/services-table'
import { TrafficChart } from '@/features/app-detail/traffic-chart'
import { useAppStatsContext } from '@/features/app-detail/stats-context'
import { useServices } from '@/lib/api/services'
import { useDomains } from '@/lib/api/domains'
import { useDeployments } from '@/lib/api/deployments'
import { durationBetween, formatBytes, formatPercent, relativeTime, shortSha } from '@/lib/format'

export function OverviewTab({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
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
          label={t('overview.totalCpu')}
          value={formatPercent(aggregate.cpu)}
          sub={t('overview.totalCpuSub')}
          accent
        />
        <StatTile
          label={t('overview.totalMemory')}
          value={formatBytes(aggregate.mem)}
          sub={t('overview.totalMemorySub')}
        />
        <StatTile
          label={t('overview.services')}
          value={`${aggregate.running} / ${aggregate.total}`}
          sub={t('overview.servicesSub')}
        />
        <StatTile
          label={t('overview.domains')}
          value={String(domains?.length ?? 0)}
          sub={t('overview.domainsSub')}
        />
      </StatStrip>

      <TrafficChart appId={appId} />

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
          <SectionHeader>{t('overview.servicesSection')}</SectionHeader>
          <SwapFade id={isLoading ? 'loading' : 'table'}>
            {isLoading ? (
              <ListSkeleton header rows={4} />
            ) : (
              <ServicesTable appId={appId} services={services ?? []} />
            )}
          </SwapFade>
        </div>

        <div className="flex flex-col gap-6 lg:w-80 lg:shrink-0">
          <OverviewPanel appId={appId} />

          <div>
            <SectionHeader>{t('overview.domainsSection')}</SectionHeader>
            <Card className="gap-0 p-0">
              {domains && domains.length > 0 ? (
                domains.map((d, i) => (
                  <div
                    key={d.id || d.hostname}
                    className={`flex items-center justify-between gap-2 px-4 py-3 ${i > 0 ? 'border-t' : ''}`}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      {d.status === 'active' ? (
                        <Lock className="size-3.5 shrink-0 text-ok" />
                      ) : (
                        <ShieldAlert className="size-3.5 shrink-0 text-warn" />
                      )}
                      <a
                        href={`https://${d.hostname}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="group flex min-w-0 items-center gap-1 font-mono text-xs hover:text-foreground hover:underline"
                      >
                        <span className="truncate">{d.hostname}</span>
                        <ExternalLink className="size-3 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                      </a>
                    </div>
                    <StatusPill status={d.status ?? 'checking'} size="sm" />
                  </div>
                ))
              ) : (
                <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                  {t('overview.noDomains')}
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
                  {t('overview.viewAll')}
                </Link>
              }
            >
              {t('overview.recentDeploys')}
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
                        {d.commit_message ?? t('overview.deployFallback')}
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
                  {t('overview.noDeployments')}
                </p>
              )}
            </Card>
          </div>
        </div>
      </div>
    </div>
  )
}
