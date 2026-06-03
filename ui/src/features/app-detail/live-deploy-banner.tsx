import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import { Card } from '@/components/ui/card'
import { StatusPill } from '@/components/common/status-pill'
import { ConnectionBadge } from '@/components/common/connection-badge'
import { LogViewer } from '@/components/common/log-viewer'
import { DeploySteps } from '@/features/app-detail/deploy-steps'
import { useDeployments } from '@/lib/api/deployments'
import { useDeploymentLogs } from '@/lib/ws/use-log-stream'
import { isDeployActive } from '@/lib/deploy-status'
import { queryKeys } from '@/lib/query/keys'
import { formatDuration, shortSha } from '@/lib/format'
import type { Deployment } from '@/types/api'

// LiveDeployBanner surfaces the app's currently-running deploy across every
// tab: pipeline step, elapsed time, and a live (pinned-tail) build log. It
// renders nothing when no deploy is in progress.
export function LiveDeployBanner({ appId }: { appId: string }) {
  const { data: deployments } = useDeployments(appId)
  // The list is newest-first, so the first active row is the current deploy.
  const active = deployments?.find((d) => isDeployActive(d.status))
  if (!active) return null
  return <ActiveDeploy key={active.id} appId={appId} deployment={active} />
}

function ActiveDeploy({ appId, deployment }: { appId: string; deployment: Deployment }) {
  const { t } = useTranslation('app-detail')
  const qc = useQueryClient()
  const elapsed = useElapsed(deployment.started_at ?? deployment.triggered_at)
  const { lines, done, status } = useDeploymentLogs(deployment.id, true, () => {
    qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
  })

  return (
    <Card className="mb-5 gap-3 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{t('liveBanner.deploying')}</span>
            <StatusPill status={deployment.status} size="sm" />
            {/* Hide the badge once the stream terminates — the socket closes by
                design then, which isn't a connectivity problem. */}
            {!done ? <ConnectionBadge status={status} /> : null}
          </div>
          <div className="mt-1 font-mono text-2xs text-muted-foreground">
            {deployment.commit_message ?? t('liveBanner.deployFallback')} ·{' '}
            {shortSha(deployment.commit_sha)} ·{' '}
            {t('liveBanner.elapsed', { duration: formatDuration(elapsed) })}
          </div>
        </div>
      </div>

      <DeploySteps status={deployment.status} />

      <LogViewer
        lines={lines}
        className="h-64"
        emptyLabel={t('liveBanner.logsEmpty')}
        label={t('liveBanner.logsLabel')}
      />
    </Card>
  )
}

// useElapsed returns whole seconds since `start`, ticking once a second while
// the deploy is live.
function useElapsed(start: string): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(id)
  }, [])
  const startMs = new Date(start).getTime()
  if (Number.isNaN(startMs)) return 0
  return Math.max(0, Math.floor((now - startMs) / 1_000))
}
