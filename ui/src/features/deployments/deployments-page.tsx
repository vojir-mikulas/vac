import { Link } from '@tanstack/react-router'
import { useQueries } from '@tanstack/react-query'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/common/empty-state'
import { DeploySteps } from '@/features/app-detail/deploy-steps'
import { useApps } from '@/lib/api/apps'
import { deploymentsApi } from '@/lib/api/deployments'
import { queryKeys } from '@/lib/query/keys'
import { durationBetween, formatDuration, relativeTime, shortSha } from '@/lib/format'
import { isDeployActive, isDeploySucceeded, isDeployTerminal } from '@/lib/deploy-status'
import type { Deployment } from '@/types/api'

interface Row extends Deployment {
  appName: string
}

export function DeploymentsPage() {
  const { data: apps } = useApps()
  const appList = apps ?? []

  const results = useQueries({
    queries: appList.map((app) => ({
      queryKey: queryKeys.apps.deployments(app.id),
      queryFn: () => deploymentsApi.list(app.id),
    })),
  })

  const isLoading = results.some((r) => r.isLoading)

  // Cheap to recompute each render (≤100 rows × app count); merge + sort inline.
  const rows: Row[] = []
  results.forEach((r, i) => {
    const app = appList[i]
    if (!app || !r.data) return
    for (const d of r.data) rows.push({ ...d, appName: app.name })
  })
  rows.sort((a, b) => new Date(b.triggered_at).getTime() - new Date(a.triggered_at).getTime())

  const metrics = computeMetrics(rows)
  const active = rows.filter((r) => isDeployActive(r.status))

  return (
    <PageContainer>
      <PageHeader title="Deployments" description="Build activity across all apps." />

      <div className="mb-6">
        <StatStrip>
          <StatTile label="Deploys today" value={String(metrics.today)} sub="triggered" accent />
          <StatTile label="Success rate" value={`${metrics.successRate}%`} sub="recent deploys" />
          <StatTile label="Avg build time" value={metrics.avgBuild} sub="successful" />
          <StatTile label="In progress" value={String(active.length)} sub="running now" />
        </StatStrip>
      </div>

      {active.length > 0 ? (
        <div className="mb-6">
          <SectionHeader>In progress</SectionHeader>
          <div className="flex flex-col gap-2">
            {active.map((d) => (
              <Card key={d.id} className="gap-3 p-4">
                <div className="flex items-center justify-between gap-3">
                  <span className="font-mono text-sm font-medium">{d.appName}</span>
                  <StatusPill status={d.status} size="sm" />
                </div>
                <DeploySteps status={d.status} />
              </Card>
            ))}
          </div>
        </div>
      ) : null}

      <SectionHeader>Timeline</SectionHeader>
      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : rows.length === 0 ? (
        <EmptyState
          title="No deployments yet"
          description="Deploys across all apps will appear here."
        />
      ) : (
        <Card className="gap-0 p-0">
          <div className="flex items-center gap-4 border-b px-5 py-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
            <span className="w-32 shrink-0">App</span>
            <span className="flex-1">Commit</span>
            <span className="shrink-0">Status</span>
          </div>
          {rows.slice(0, 50).map((d, i) => (
            <div
              key={d.id}
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
                <div className="truncate text-sm">{d.commit_message ?? 'Deploy'}</div>
                <div className="font-mono text-2xs text-muted-foreground">
                  {shortSha(d.commit_sha)} · {relativeTime(d.triggered_at)} ·{' '}
                  {durationBetween(d.started_at, d.finished_at)}
                </div>
              </div>
              <StatusPill status={d.status} size="sm" />
            </div>
          ))}
        </Card>
      )}
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
    successRate: terminal > 0 ? Math.round((success / terminal) * 100) : 100,
    avgBuild: buildCount > 0 ? formatDuration(buildTotal / buildCount / 1000) : '—',
  }
}
