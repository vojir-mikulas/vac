import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'
import type { WsStatus } from '@/lib/ws/use-websocket'

// Tiny dot+label for a passive WS stream's connection state, so a silently
// reconnecting socket (backoff up to 10s) reads as "Reconnecting" instead of a
// frozen panel. Mirrors shell-dialog's idle/connecting/connected/disconnected
// language. The label carries the text, so the dot needs no extra aria.
const TONE: Record<WsStatus, string> = {
  open: 'bg-ok',
  connecting: 'bg-warn',
  reconnecting: 'bg-warn animate-pulse',
  closed: 'bg-err-foreground',
}

const LABEL_KEY: Record<WsStatus, 'live' | 'connecting' | 'reconnecting' | 'disconnected'> = {
  open: 'live',
  connecting: 'connecting',
  reconnecting: 'reconnecting',
  closed: 'disconnected',
}

export function ConnectionBadge({ status, className }: { status: WsStatus; className?: string }) {
  const { t } = useTranslation()
  return (
    <span className={cn('flex items-center gap-1.5 text-2xs text-muted-foreground', className)}>
      <span className={cn('size-1.5 rounded-full', TONE[status])} />
      {t(`connection.${LABEL_KEY[status]}`)}
    </span>
  )
}
