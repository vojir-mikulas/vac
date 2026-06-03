import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Layers, X } from 'lucide-react'

import { StatusPill } from '@/components/common/status-pill'
import { EmptyState } from '@/components/common/empty-state'
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
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { useActiveDeployments, useCancelDeployment } from '@/lib/api/deployments'
import { isDeployActive, isDeployTerminal } from '@/lib/deploy-status'
import { relativeTime, shortSha } from '@/lib/format'
import { queryKeys } from '@/lib/query/keys'
import { useWebSocket } from '@/lib/ws/use-websocket'
import { cn } from '@/lib/utils'
import type { ActiveDeployment } from '@/types/api'

// DeployQueueButton is the topbar entry point: an icon button with a live count
// badge that opens the deploy-queue side panel. It owns the live subscription so
// the badge stays current even while the panel is closed.
export function DeployQueueButton() {
  const [open, setOpen] = useState(false)
  const qc = useQueryClient()
  const { data } = useActiveDeployments()

  // Push live snapshots into the query cache on every deploy-state change. One
  // connection, mounted once in the shell; the hook pauses while the tab hidden.
  useWebSocket('deployments/stream', {
    onFrame: (frame) => {
      if (frame.type === 'deployments') {
        qc.setQueryData(queryKeys.deployments.active, frame.data as ActiveDeployment[])
      }
    },
  })

  const count = data?.length ?? 0

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <button
          type="button"
          aria-label={`Deploy queue${count > 0 ? ` — ${count} active` : ''}`}
          className="relative grid size-8 shrink-0 cursor-pointer place-items-center rounded-md border bg-background text-muted-foreground transition-colors hover:border-border-strong hover:text-foreground"
        >
          <Layers className="size-4" />
          {count > 0 ? (
            <span className="absolute -right-1.5 -top-1.5 grid min-w-4 place-items-center rounded-full bg-brand px-1 text-2xs font-semibold leading-4 text-brand-foreground">
              {count}
            </span>
          ) : null}
        </button>
      </SheetTrigger>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>Deploy queue</SheetTitle>
        </SheetHeader>
        <QueueBody />
      </SheetContent>
    </Sheet>
  )
}

function QueueBody() {
  const { data, isLoading } = useActiveDeployments()

  if (isLoading || !data) {
    return (
      <div className="flex flex-col gap-3 p-4">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    )
  }

  if (data.length === 0) {
    return (
      <div className="p-4">
        <EmptyState
          icon={Layers}
          title="Nothing deploying"
          description="Running and queued deploys across all your apps show up here."
        />
      </div>
    )
  }

  // started_at distinguishes "running" (a worker picked it up) from "queued"
  // (waiting for a free worker slot). The list arrives FIFO from the server.
  const running = data.filter((d) => d.started_at && isDeployActive(d.status))
  const queued = data.filter((d) => !d.started_at)

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
      <Group title="Running" count={running.length}>
        {running.map((d) => (
          <QueueRow key={d.id} d={d} />
        ))}
      </Group>
      <Group title="Queued" count={queued.length}>
        {queued.map((d) => (
          <QueueRow key={d.id} d={d} queued />
        ))}
      </Group>
    </div>
  )
}

function Group({
  title,
  count,
  children,
}: {
  title: string
  count: number
  children: React.ReactNode
}) {
  if (count === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {title}
        <span className="rounded-full bg-surface-2 px-1.5 text-2xs">{count}</span>
      </div>
      <div className="flex flex-col gap-2">{children}</div>
    </div>
  )
}

function QueueRow({ d, queued }: { d: ActiveDeployment; queued?: boolean }) {
  const cancel = useCancelDeployment()
  const subtitle = d.commit_message?.split('\n')[0] || shortSha(d.commit_sha) || 'latest commit'
  const when = queued
    ? `queued ${relativeTime(d.triggered_at)}`
    : `started ${relativeTime(d.started_at)}`

  return (
    <div className="flex items-start gap-3 rounded-xl border bg-surface-1 p-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <Link
            to="/apps/$appId/deploys"
            params={{ appId: d.app_id }}
            className="truncate text-sm font-medium hover:underline"
          >
            {d.app_name}
          </Link>
          {d.triggered_by === 'rollback' ? (
            <span className="rounded bg-surface-2 px-1.5 text-2xs text-muted-foreground">
              rollback
            </span>
          ) : null}
        </div>
        <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</p>
        <div className="mt-2 flex items-center gap-2">
          <StatusPill status={d.status} size="sm" />
          <span className="text-2xs text-muted-foreground">{when}</span>
        </div>
      </div>
      <CancelButton
        appId={d.app_id}
        did={d.id}
        appName={d.app_name}
        pending={cancel.isPending}
        disabled={isDeployTerminal(d.status)}
        onConfirm={() => cancel.mutate({ appId: d.app_id, did: d.id })}
      />
    </div>
  )
}

function CancelButton({
  appName,
  pending,
  disabled,
  onConfirm,
}: {
  appId: string
  did: string
  appName: string
  pending: boolean
  disabled: boolean
  onConfirm: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="danger"
          size="icon-sm"
          aria-label={`Cancel deploy of ${appName}`}
          disabled={disabled || pending}
          className={cn('shrink-0')}
        >
          <X className="size-4" />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Cancel this deploy?</AlertDialogTitle>
          <AlertDialogDescription>
            Stops the {appName} deploy. The currently running version keeps serving — cancelling
            never tears down what's already live.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep deploying</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>Cancel deploy</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
