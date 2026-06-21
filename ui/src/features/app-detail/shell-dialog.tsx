import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
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
import { api } from '@/lib/api/client'
import { useContainerShell } from '@/features/app-detail/use-container-shell'

const STATUS_KEYS = ['idle', 'connecting', 'connected', 'disconnected'] as const

function isStatusKey(s: string): s is (typeof STATUS_KEYS)[number] {
  return (STATUS_KEYS as readonly string[]).includes(s)
}

// Per-service interactive shell (P3.4). Gated behind a confirm because it opens
// a root-capable shell into a user app container from the sandboxed control
// plane; the session is audit-logged server-side. Only rendered when the
// feature flag is on and the service is running (caller's job).
export function ShellDialog({ appId, service }: { appId: string; service: string }) {
  const { t } = useTranslation('app-detail')
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

  const begin = async () => {
    // Step-up preflight over REST: a failed WS upgrade can't surface the
    // step_up_required challenge, so re-auth here first (the api client opens the
    // global step-up modal on 403). Only open the socket once it succeeds.
    try {
      await api.post(`apps/${appId}/services/${service}/exec/preflight`)
    } catch {
      return // step-up cancelled or failed — don't open the shell.
    }
    setStarted(true)
    // Wait a frame so the terminal container is laid out before xterm opens/fits.
    requestAnimationFrame(connect)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm">
          <TerminalSquare className="size-3.5" />
          {t('shell.trigger')}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="font-mono">{t('shell.title', { service })}</DialogTitle>
          <DialogDescription>{t('shell.description')}</DialogDescription>
        </DialogHeader>

        {!started ? (
          <div className="flex flex-col gap-4">
            <div className="flex items-start gap-2.5 rounded-md border border-warn-border bg-warn-bg p-3 text-sm text-warn-foreground">
              <ShieldAlert className="mt-0.5 size-4 shrink-0" />
              <p>
                <Trans
                  t={t}
                  i18nKey="shell.warning"
                  values={{ service }}
                  components={[<span className="font-mono" />]}
                />
              </p>
            </div>
            <div className="flex justify-end">
              <Button variant="brand" onClick={begin}>
                <TerminalSquare className="size-4" />
                {t('shell.openShell')}
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
                {isStatusKey(status) ? t(`shell.status.${status}`) : status}
              </span>
              {status === 'disconnected' ? (
                <Button variant="outline" size="xs" onClick={connect}>
                  {t('shell.reconnect')}
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
