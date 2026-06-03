import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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
import { CardStackSkeleton } from '@/components/common/card-stack-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
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
  const { t } = useTranslation('app-detail')
  const { data: deployments, isLoading, isError, refetch } = useDeployments(appId)

  // The newest successful deployment is the version currently live — rolling
  // back to it is a no-op, so the Roll back action is hidden on that row.
  const currentId = deployments?.find((d) => isDeploySucceeded(d.status))?.id

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader className="mb-0">{t('deploys.history')}</SectionHeader>

      <SwapFade
        id={
          isLoading
            ? 'loading'
            : isError
              ? 'error'
              : deployments && deployments.length > 0
                ? 'rows'
                : 'empty'
        }
      >
        {isLoading ? (
          <CardStackSkeleton count={5} rowHeight="h-12" gap="gap-2" />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : deployments && deployments.length > 0 ? (
          <div className="flex flex-col gap-2">
            {deployments.map((d) => (
              <DeployRow key={d.id} deployment={d} isCurrent={d.id === currentId} />
            ))}
          </div>
        ) : (
          <EmptyState title={t('deploys.emptyTitle')} description={t('deploys.emptyDescription')} />
        )}
      </SwapFade>
    </div>
  )
}

function DeployRow({ deployment, isCurrent }: { deployment: Deployment; isCurrent: boolean }) {
  const { t } = useTranslation('app-detail')
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
            {deployment.commit_message ?? t('deploys.deployFallback')}
          </div>
          <div className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
            {deployment.triggered_by === 'rollback' ? (
              <span className="inline-flex items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 font-sans font-medium text-foreground">
                <RotateCcw className="size-3" />
                {t('deploys.rollback')}
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
            {t('deploys.live')}
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
  const { t } = useTranslation('app-detail')
  const rollback = useRollbackDeploy(deployment.app_id)
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border bg-muted/40 px-3 py-2">
      <p className="text-xs text-muted-foreground">
        {deployment.commit_sha
          ? t('deploys.rollbackPromptWithSha', { sha: shortSha(deployment.commit_sha) })
          : t('deploys.rollbackPrompt')}
      </p>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" size="sm" disabled={rollback.isPending}>
            <RotateCcw className="size-3.5" />
            {t('deploys.rollBack')}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('deploys.rollbackDialogTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('deploys.rollbackDialogDescription', { sha: shortSha(deployment.commit_sha) })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                rollback.mutate(deployment.id, {
                  onSuccess: () => toast.success(t('deploys.rollbackTriggered')),
                  onError: (e) => toast.error(e.message),
                })
              }
            >
              {t('deploys.rollBack')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function BuildLogs({ appId, did }: { appId: string; did: string }) {
  const { t } = useTranslation('app-detail')
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
      emptyLabel={t('deploys.buildLogsEmpty')}
      label={t('deploys.buildLogsLabel')}
    />
  )
}
