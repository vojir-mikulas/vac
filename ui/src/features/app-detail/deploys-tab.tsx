import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronDown, RotateCw } from 'lucide-react'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/common/empty-state'
import { SectionHeader } from '@/components/common/section-header'
import { StatusPill } from '@/components/common/status-pill'
import { LogViewer } from '@/components/common/log-viewer'
import { DeploySteps } from '@/features/app-detail/deploy-steps'
import { useDeployments, useTriggerDeploy } from '@/lib/api/deployments'
import { useDeploymentLogs } from '@/lib/ws/use-log-stream'
import { queryKeys } from '@/lib/query/keys'
import { cn } from '@/lib/utils'
import { durationBetween, relativeTime, shortSha } from '@/lib/format'
import type { Deployment } from '@/types/api'

export function DeploysTab({ appId }: { appId: string }) {
  const { data: deployments, isLoading } = useDeployments(appId)
  const deploy = useTriggerDeploy(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">History</SectionHeader>
        <Button
          variant="brand"
          size="sm"
          disabled={deploy.isPending}
          onClick={() =>
            deploy.mutate(undefined, {
              onSuccess: () => toast.success('Deploy triggered'),
              onError: (e) => toast.error(e.message),
            })
          }
        >
          <RotateCw className="size-3.5" />
          Deploy from HEAD
        </Button>
      </div>

      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : deployments && deployments.length > 0 ? (
        <div className="flex flex-col gap-2">
          {deployments.map((d) => (
            <DeployRow key={d.id} deployment={d} />
          ))}
        </div>
      ) : (
        <EmptyState
          title="No deployments yet"
          description="Trigger a deploy from your configured branch to get started."
        />
      )}
    </div>
  )
}

function DeployRow({ deployment }: { deployment: Deployment }) {
  const [open, setOpen] = useState(false)

  return (
    <Card className="gap-0 p-0">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex cursor-pointer items-center gap-4 px-5 py-3.5 text-left"
      >
        <ChevronDown
          className={cn(
            'size-4 shrink-0 text-muted-foreground transition-transform',
            open && 'rotate-180',
          )}
        />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">
            {deployment.commit_message ?? 'Deploy'}
          </div>
          <div className="font-mono text-2xs text-muted-foreground">
            {shortSha(deployment.commit_sha)} · {relativeTime(deployment.triggered_at)} ·{' '}
            {durationBetween(deployment.started_at, deployment.finished_at)}
          </div>
        </div>
        <StatusPill status={deployment.status} size="sm" />
      </button>

      {open ? (
        <div className="flex flex-col gap-3 border-t px-5 py-4">
          <DeploySteps status={deployment.status} />
          {deployment.error ? (
            <p className="rounded-md border border-err-border bg-err-bg px-3 py-2 font-mono text-xs text-err-foreground">
              {deployment.error}
            </p>
          ) : null}
          <BuildLogs appId={deployment.app_id} did={deployment.id} />
        </div>
      ) : null}
    </Card>
  )
}

function BuildLogs({ appId, did }: { appId: string; did: string }) {
  const qc = useQueryClient()
  // When the build stream terminates, settle the deployment list so the row's
  // status flips to its terminal value immediately rather than on the next poll.
  const { lines } = useDeploymentLogs(did, true, () => {
    qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
  })
  return <LogViewer lines={lines} className="h-80" emptyLabel="No build output." />
}
