import { Link } from '@tanstack/react-router'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionHeader } from '@/components/common/section-header'
import { StatusPill } from '@/components/common/status-pill'
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
import { DeploySteps } from '@/features/app-detail/deploy-steps'
import { useActiveDeployments, useCancelDeployment } from '@/lib/api/deployments'
import { isDeployActive, isDeployTerminal } from '@/lib/deploy-status'
import { relativeTime, shortSha } from '@/lib/format'
import type { ActiveDeployment } from '@/types/api'

type DepT = ReturnType<typeof useTranslation<'deployments'>>['t']

// DeployQueue is the instance-wide queue, rendered inline on the Deployments
// page. It splits the live snapshot into "In progress" (a worker picked it up —
// shown with its pipeline steps) and "Queued" (waiting for a free worker slot).
// Renders nothing when nothing is deploying; the page's timeline carries history.
export function DeployQueue() {
  const { t } = useTranslation('deployments')
  const { data } = useActiveDeployments()
  if (!data || data.length === 0) return null

  // started_at distinguishes "running" (picked up) from "queued" (waiting). The
  // list arrives FIFO from the server.
  const running = data.filter((d) => d.started_at && isDeployActive(d.status))
  const queued = data.filter((d) => !d.started_at)

  return (
    <div className="mb-6 flex flex-col gap-6">
      {running.length > 0 ? (
        <div>
          <SectionHeader>{t('queue.inProgress')}</SectionHeader>
          <div className="flex flex-col gap-2">
            {running.map((d) => (
              <RunningCard key={d.id} d={d} />
            ))}
          </div>
        </div>
      ) : null}
      {queued.length > 0 ? (
        <div>
          <SectionHeader>{t('queue.queued')}</SectionHeader>
          <div className="flex flex-col gap-2">
            {queued.map((d) => (
              <QueuedRow key={d.id} d={d} />
            ))}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function AppLink({ d }: { d: ActiveDeployment }) {
  const { t } = useTranslation('deployments')
  return (
    <div className="flex items-center gap-2">
      <Link
        to="/apps/$appId/deploys"
        params={{ appId: d.app_id }}
        className="truncate font-mono text-sm font-medium hover:underline"
      >
        {d.app_name}
      </Link>
      {d.triggered_by === 'rollback' ? (
        <span className="rounded bg-surface-2 px-1.5 text-2xs text-muted-foreground">
          {t('queue.rollbackBadge')}
        </span>
      ) : null}
    </div>
  )
}

function subtitle(d: ActiveDeployment, t: DepT) {
  return d.commit_message?.split('\n')[0] || shortSha(d.commit_sha) || t('queue.latestCommit')
}

function RunningCard({ d }: { d: ActiveDeployment }) {
  const { t } = useTranslation('deployments')
  return (
    <Card className="gap-3 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <AppLink d={d} />
          <p className="mt-0.5 truncate text-xs text-muted-foreground">
            {subtitle(d, t)} · {t('queue.startedAt', { time: relativeTime(d.started_at) })}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <StatusPill status={d.status} size="sm" />
          <CancelButton d={d} />
        </div>
      </div>
      <DeploySteps status={d.status} />
    </Card>
  )
}

function QueuedRow({ d }: { d: ActiveDeployment }) {
  const { t } = useTranslation('deployments')
  return (
    <div className="flex items-start gap-3 rounded-xl border bg-surface-1 p-3">
      <div className="min-w-0 flex-1">
        <AppLink d={d} />
        <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle(d, t)}</p>
        <div className="mt-2 flex items-center gap-2">
          <StatusPill status={d.status} size="sm" />
          <span className="text-2xs text-muted-foreground">
            {t('queue.queuedAt', { time: relativeTime(d.triggered_at) })}
          </span>
        </div>
      </div>
      <CancelButton d={d} />
    </div>
  )
}

function CancelButton({ d }: { d: ActiveDeployment }) {
  const { t } = useTranslation('deployments')
  const cancel = useCancelDeployment()
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="danger"
          size="icon-sm"
          aria-label={t('cancel.ariaLabel', { app: d.app_name })}
          disabled={isDeployTerminal(d.status) || cancel.isPending}
          className="shrink-0"
        >
          <X className="size-4" />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('cancel.title')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('cancel.description', { app: d.app_name })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('cancel.keepDeploying')}</AlertDialogCancel>
          <AlertDialogAction onClick={() => cancel.mutate({ appId: d.app_id, did: d.id })}>
            {t('cancel.confirm')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
