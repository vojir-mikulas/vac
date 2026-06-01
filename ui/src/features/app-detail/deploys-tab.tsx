import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronDown, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/common/empty-state'
import { SectionHeader } from '@/components/common/section-header'
import { StatusPill } from '@/components/common/status-pill'
import { LogViewer } from '@/components/common/log-viewer'
import { DeploySteps } from '@/features/app-detail/deploy-steps'
import { useDeployments, useRollbackDeploy } from '@/lib/api/deployments'
import { useDeploymentLogs } from '@/lib/ws/use-log-stream'
import { isDeploySucceeded } from '@/lib/deploy-status'
import { queryKeys } from '@/lib/query/keys'
import { cn } from '@/lib/utils'
import { durationBetween, relativeTime, shortSha } from '@/lib/format'
import type { Deployment } from '@/types/api'

export function DeploysTab({ appId }: { appId: string }) {
  const { data: deployments, isLoading } = useDeployments(appId)

  // The newest successful deployment is the version currently live — rolling
  // back to it is a no-op, so the Roll back action is hidden on that row.
  const currentId = deployments?.find((d) => isDeploySucceeded(d.status))?.id

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader className="mb-0">History</SectionHeader>

      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : deployments && deployments.length > 0 ? (
        <div className="flex flex-col gap-2">
          {deployments.map((d) => (
            <DeployRow key={d.id} deployment={d} isCurrent={d.id === currentId} />
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

function DeployRow({ deployment, isCurrent }: { deployment: Deployment; isCurrent: boolean }) {
  const [open, setOpen] = useState(false)
  // Offer rollback on prior successful deployments only — never the live one
  // (that would redeploy the same commit) and never a failed attempt.
  const canRollBack = isDeploySucceeded(deployment.status) && !isCurrent

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
          <div className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
            {deployment.triggered_by === 'rollback' ? (
              <span className="inline-flex items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 font-sans font-medium text-foreground">
                <RotateCcw className="size-3" />
                Rollback
              </span>
            ) : null}
            <span>
              {shortSha(deployment.commit_sha)} · {relativeTime(deployment.triggered_at)} ·{' '}
              {durationBetween(deployment.started_at, deployment.finished_at)}
            </span>
          </div>
        </div>
        {isCurrent ? (
          <span className="rounded-sm bg-ok-bg px-1.5 py-0.5 text-2xs font-medium text-ok-foreground">
            Live
          </span>
        ) : null}
        <StatusPill status={deployment.status} size="sm" />
      </button>

      {open ? (
        <div className="flex flex-col gap-3 border-t px-5 py-4">
          {canRollBack ? <RollBackAction deployment={deployment} /> : null}
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

function RollBackAction({ deployment }: { deployment: Deployment }) {
  const rollback = useRollbackDeploy(deployment.app_id)
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border bg-muted/40 px-3 py-2">
      <p className="text-xs text-muted-foreground">
        Redeploy this commit{deployment.commit_sha ? ` (${shortSha(deployment.commit_sha)})` : ''}.
      </p>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" size="sm" disabled={rollback.isPending}>
            <RotateCcw className="size-3.5" />
            Roll back
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Roll back to this deployment?</AlertDialogTitle>
            <AlertDialogDescription>
              This rebuilds and redeploys commit {shortSha(deployment.commit_sha)} as a new
              deployment. Only the code is rolled back — environment variables are left unchanged.
              The current version keeps serving until the rollback is healthy.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                rollback.mutate(deployment.id, {
                  onSuccess: () => toast.success('Rollback triggered'),
                  onError: (e) => toast.error(e.message),
                })
              }
            >
              Roll back
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function BuildLogs({ appId, did }: { appId: string; did: string }) {
  const qc = useQueryClient()
  // When the build stream terminates, settle the deployment list so the row's
  // status flips to its terminal value immediately rather than on the next poll.
  const { lines } = useDeploymentLogs(did, true, () => {
    qc.invalidateQueries({ queryKey: queryKeys.apps.deployments(appId) })
  })
  return (
    <LogViewer
      lines={lines}
      className="h-80"
      emptyLabel="No build output."
      label="Deployment logs"
    />
  )
}
