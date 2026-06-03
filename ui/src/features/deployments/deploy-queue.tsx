import { Link } from '@tanstack/react-router'
import { X } from 'lucide-react'

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

// DeployQueue is the instance-wide queue, rendered inline on the Deployments
// page. It splits the live snapshot into "In progress" (a worker picked it up —
// shown with its pipeline steps) and "Queued" (waiting for a free worker slot).
// Renders nothing when nothing is deploying; the page's timeline carries history.
export function DeployQueue() {
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
          <SectionHeader>In progress</SectionHeader>
          <div className="flex flex-col gap-2">
            {running.map((d) => (
              <RunningCard key={d.id} d={d} />
            ))}
          </div>
        </div>
      ) : null}
      {queued.length > 0 ? (
        <div>
          <SectionHeader>Queued</SectionHeader>
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
        <span className="rounded bg-surface-2 px-1.5 text-2xs text-muted-foreground">rollback</span>
      ) : null}
    </div>
  )
}

function subtitle(d: ActiveDeployment) {
  return d.commit_message?.split('\n')[0] || shortSha(d.commit_sha) || 'latest commit'
}

function RunningCard({ d }: { d: ActiveDeployment }) {
  return (
    <Card className="gap-3 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <AppLink d={d} />
          <p className="mt-0.5 truncate text-xs text-muted-foreground">
            {subtitle(d)} · started {relativeTime(d.started_at)}
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
  return (
    <div className="flex items-start gap-3 rounded-xl border bg-surface-1 p-3">
      <div className="min-w-0 flex-1">
        <AppLink d={d} />
        <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle(d)}</p>
        <div className="mt-2 flex items-center gap-2">
          <StatusPill status={d.status} size="sm" />
          <span className="text-2xs text-muted-foreground">
            queued {relativeTime(d.triggered_at)}
          </span>
        </div>
      </div>
      <CancelButton d={d} />
    </div>
  )
}

function CancelButton({ d }: { d: ActiveDeployment }) {
  const cancel = useCancelDeployment()
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="danger"
          size="icon-sm"
          aria-label={`Cancel deploy of ${d.app_name}`}
          disabled={isDeployTerminal(d.status) || cancel.isPending}
          className="shrink-0"
        >
          <X className="size-4" />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Cancel this deploy?</AlertDialogTitle>
          <AlertDialogDescription>
            Stops the {d.app_name} deploy. The currently running version keeps serving — cancelling
            never tears down what's already live.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep deploying</AlertDialogCancel>
          <AlertDialogAction onClick={() => cancel.mutate({ appId: d.app_id, did: d.id })}>
            Cancel deploy
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
