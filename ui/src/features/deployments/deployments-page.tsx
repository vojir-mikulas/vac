import { useMemo } from 'react'
import { m } from 'motion/react'
import { Link } from '@tanstack/react-router'
import { useQueries } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { Card } from '@/components/ui/card'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { listItem } from '@/lib/motion'
import { DeployQueue } from '@/features/deployments/deploy-queue'
import { useApps } from '@/lib/api/apps'
import { deploymentsApi, useActiveDeployments } from '@/lib/api/deployments'
import { queryKeys } from '@/lib/query/keys'
import { durationBetween, formatDuration, relativeTime, shortSha } from '@/lib/format'
import { isDeploySucceeded, isDeployTerminal } from '@/lib/deploy-status'
import type { Deployment } from '@/types/api'

interface Row extends Deployment {
  appName: string
}

export function DeploymentsPage() {
  const { t } = useTranslation('deployments')
  const { data: apps, isError: appsError, refetch: refetchApps } = useApps()
  const appList = apps ?? []
  const { data: queue } = useActiveDeployments()

  const results = useQueries({
    queries: appList.map((app) => ({
      queryKey: queryKeys.apps.deployments(app.id),
      queryFn: () => deploymentsApi.list(app.id),
      // The fan-out is one query per app; without this every visit refetches N
      // times. The deploy WS pushes invalidations, so a short window is safe.
      staleTime: 30_000,
    })),
  })

  const isLoading = results.some((r) => r.isLoading)
  // The timeline fails if the apps list itself failed, or every per-app deploy
  // query did — a partial failure still renders the apps that loaded.
  const isError = appsError || (results.length > 0 && results.every((r) => r.isError))
  const refetch = () => {
    refetchApps()
    results.forEach((r) => r.refetch())
  }

  // Merge + sort the per-app deploy lists. React Query hands back stable `data`
  // references via structural sharing, so keying the memo on each query's
  // `dataUpdatedAt` recomputes only when a list actually changes — not on every
  // render (the `results` array itself is fresh each render).
  const dataSig = results.map((r) => r.dataUpdatedAt).join(',')
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    results.forEach((r, i) => {
      const app = appList[i]
      if (!app || !r.data) return
      for (const d of r.data) out.push({ ...d, appName: app.name })
    })
    out.sort((a, b) => new Date(b.triggered_at).getTime() - new Date(a.triggered_at).getTime())
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appList, dataSig])

  const metrics = useMemo(() => computeMetrics(rows), [rows])
  const activeCount = queue?.length ?? 0

  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />

      <div className="mb-6">
        <StatStrip>
          <StatTile
            label={t('page.stats.deploysToday')}
            value={String(metrics.today)}
            sub={t('page.stats.deploysTodaySub')}
            accent
          />
          <StatTile
            label={t('page.stats.successRate')}
            value={metrics.successRate === null ? '—' : `${metrics.successRate}%`}
            sub={t('page.stats.successRateSub')}
          />
          <StatTile
            label={t('page.stats.avgBuildTime')}
            value={metrics.avgBuild}
            sub={t('page.stats.avgBuildTimeSub')}
          />
          <StatTile
            label={t('page.stats.inProgress')}
            value={String(activeCount)}
            sub={t('page.stats.inProgressSub')}
          />
        </StatStrip>
      </div>

      <DeployQueue />

      <SectionHeader>{t('page.timeline')}</SectionHeader>
      <SwapFade
        id={isLoading ? 'loading' : isError ? 'error' : rows.length === 0 ? 'empty' : 'timeline'}
      >
        {isLoading ? (
          <ListSkeleton header rows={6} />
        ) : isError ? (
          <ErrorState onRetry={refetch} />
        ) : rows.length === 0 ? (
          <EmptyState title={t('page.empty.title')} description={t('page.empty.description')} />
        ) : (
          <Card className="gap-0 p-0">
            <div className="flex items-center gap-4 border-b bg-surface-1 px-5 py-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
              <span className="w-32 shrink-0">{t('page.table.app')}</span>
              <span className="flex-1">{t('page.table.commit')}</span>
              <span className="shrink-0">{t('page.table.status')}</span>
            </div>
            {rows.slice(0, 50).map((d, i) => (
              <m.div
                key={d.id}
                custom={i}
                variants={listItem}
                initial="hidden"
                animate="visible"
                className={`flex items-center gap-4 px-5 py-3 ${i > 0 ? 'border-t' : ''}`}
              >
                <Link
                  to="/apps/$appId/deploys"
                  params={{ appId: d.app_id }}
                  className="w-32 shrink-0 truncate font-mono text-xs font-medium hover:underline"
                >
                  {d.appName}
                </Link>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm">
                    {d.commit_message ?? t('page.deployFallback')}
                  </div>
                  <div className="font-mono text-2xs text-muted-foreground">
                    {shortSha(d.commit_sha)} · {relativeTime(d.triggered_at)} ·{' '}
                    {durationBetween(d.started_at, d.finished_at)}
                  </div>
                </div>
                <StatusPill status={d.status} size="sm" />
              </m.div>
            ))}
            {rows.length > 50 ? (
              <div className="border-t px-5 py-2.5 text-center text-2xs text-muted-foreground">
                {t('page.timelineTruncated', { shown: 50, total: rows.length })}
              </div>
            ) : null}
          </Card>
        )}
      </SwapFade>
    </PageContainer>
  )
}

function computeMetrics(rows: Row[]) {
  const startOfDay = new Date()
  startOfDay.setHours(0, 0, 0, 0)
  const startMs = startOfDay.getTime()

  let today = 0
  let success = 0
  let terminal = 0
  let buildTotal = 0
  let buildCount = 0

  for (const d of rows) {
    if (new Date(d.triggered_at).getTime() >= startMs) today++
    if (isDeployTerminal(d.status)) {
      terminal++
      if (isDeploySucceeded(d.status)) success++
    }
    if (isDeploySucceeded(d.status) && d.started_at && d.finished_at) {
      const ms = new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()
      if (ms > 0) {
        buildTotal += ms
        buildCount++
      }
    }
  }

  return {
    today,
    // null (rendered as "—") when nothing has finished yet, so a brand-new box
    // doesn't misleadingly read "100% success" having never deployed.
    successRate: terminal > 0 ? Math.round((success / terminal) * 100) : null,
    avgBuild: buildCount > 0 ? formatDuration(buildTotal / buildCount / 1000) : '—',
  }
}
