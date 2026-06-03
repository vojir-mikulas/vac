import { useState } from 'react'
import { ShieldAlert, TerminalSquare } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { useContainerShell } from '@/features/app-detail/use-container-shell'

const STATUS_LABEL: Record<string, string> = {
  idle: 'Idle',
  connecting: 'Connecting…',
  connected: 'Connected',
  disconnected: 'Disconnected',
}

// Per-service interactive shell (P3.4). Gated behind a confirm because it opens
// a root-capable shell into a user app container from the sandboxed control
// plane; the session is audit-logged server-side. Only rendered when the
// feature flag is on and the service is running (caller's job).
export function ShellDialog({ appId, service }: { appId: string; service: string }) {
  const [open, setOpen] = useState(false)
  const [started, setStarted] = useState(false)
  const { containerRef, status, connect, disconnect } = useContainerShell(appId, service)

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      disconnect()
      setStarted(false)
    }
  }

  const begin = () => {
    setStarted(true)
    // Wait a frame so the terminal container is laid out before xterm opens/fits.
    requestAnimationFrame(connect)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm">
          <TerminalSquare className="size-3.5" />
          Shell
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="font-mono">Shell · {service}</DialogTitle>
          <DialogDescription>This session is recorded in the audit log.</DialogDescription>
        </DialogHeader>

        {!started ? (
          <div className="flex flex-col gap-4">
            <div className="flex items-start gap-2.5 rounded-md border border-warn-border bg-warn-bg p-3 text-sm text-warn-foreground">
              <ShieldAlert className="mt-0.5 size-4 shrink-0" />
              <p>
                Open a root-capable shell into <span className="font-mono">{service}</span>? You
                will have full access inside the container. The session open is audit-logged.
              </p>
            </div>
            <div className="flex justify-end">
              <Button variant="brand" onClick={begin}>
                <TerminalSquare className="size-4" />
                Open shell
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between text-2xs text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <span
                  className={`size-1.5 rounded-full ${
                    status === 'connected'
                      ? 'bg-ok'
                      : status === 'connecting'
                        ? 'bg-warn'
                        : 'bg-err-foreground'
                  }`}
                />
                {STATUS_LABEL[status] ?? status}
              </span>
              {status === 'disconnected' ? (
                <Button variant="outline" size="xs" onClick={connect}>
                  Reconnect
                </Button>
              ) : null}
            </div>
            <div
              ref={containerRef}
              className="h-96 w-full overflow-hidden rounded-md bg-[#0a0a0a] p-2"
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
