import { Play, RotateCw, Square } from 'lucide-react'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { useStackControl } from '@/lib/api/apps'
import type { AppStatus } from '@/types/api'

type StackAction = 'start' | 'stop' | 'restart'

// Shared start/stop/restart-all cluster. Rendered both compact (icon-only) in
// the app-detail header and labelled in the Services tab, so the mutation logic
// lives in one place. Enable/disable is gated on the app status: Start is moot
// while running, Stop while stopped, and everything is locked mid-build.
export function StackControls({
  appId,
  status,
  compact = false,
}: {
  appId: string
  status?: AppStatus
  compact?: boolean
}) {
  const stack = useStackControl(appId)

  const run = (action: StackAction) =>
    stack.mutate(action, {
      onSuccess: () => toast.success(`Stack ${action}ed`),
      onError: (e) => toast.error(e.message),
    })

  const busy = stack.isPending || status === 'building'
  const startDisabled = busy || status === 'running'
  const restartDisabled = busy || status === 'stopped'
  const stopDisabled = busy || status === 'stopped'

  if (compact) {
    return (
      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon-sm"
          title="Start"
          aria-label="Start stack"
          disabled={startDisabled}
          onClick={() => run('start')}
        >
          <Play className="size-3.5" />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          title="Restart all"
          aria-label="Restart stack"
          disabled={restartDisabled}
          onClick={() => run('restart')}
        >
          <RotateCw className="size-3.5" />
        </Button>
        <Button
          variant="danger"
          size="icon-sm"
          title="Stop"
          aria-label="Stop stack"
          disabled={stopDisabled}
          onClick={() => run('stop')}
        >
          <Square className="size-3.5" />
        </Button>
      </div>
    )
  }

  return (
    <div className="flex gap-2">
      <Button variant="outline" size="sm" disabled={startDisabled} onClick={() => run('start')}>
        <Play className="size-3.5" />
        Start
      </Button>
      <Button variant="outline" size="sm" disabled={restartDisabled} onClick={() => run('restart')}>
        <RotateCw className="size-3.5" />
        Restart
      </Button>
      <Button variant="danger" size="sm" disabled={stopDisabled} onClick={() => run('stop')}>
        <Square className="size-3.5" />
        Stop
      </Button>
    </div>
  )
}
