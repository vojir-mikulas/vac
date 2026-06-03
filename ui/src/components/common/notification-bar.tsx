import type { ReactNode } from 'react'
import { m } from 'motion/react'
import { CheckCircle2, Info, TriangleAlert, XCircle, type LucideIcon } from 'lucide-react'

import { cn } from '@/lib/utils'
import { RISE, transition } from '@/lib/motion'

export type NotificationTone = 'info' | 'success' | 'warn' | 'error'

const TONES: Record<NotificationTone, { Icon: LucideIcon; container: string; accent: string }> = {
  info: { Icon: Info, container: 'border-info-border bg-info-bg', accent: 'text-info-foreground' },
  success: {
    Icon: CheckCircle2,
    container: 'border-ok-border bg-ok-bg',
    accent: 'text-ok-foreground',
  },
  warn: {
    Icon: TriangleAlert,
    container: 'border-warn-border bg-warn-bg',
    accent: 'text-warn-foreground',
  },
  error: { Icon: XCircle, container: 'border-err-border bg-err-bg', accent: 'text-err-foreground' },
}

// Inline, tone-coded callout for guidance and async results (used by the create
// wizard). It rises in on mount; to animate its removal, wrap it in
// <AnimatePresence> at the call site and give it a stable `key`. Pass `icon` to
// override the tone's default glyph (e.g. a spinner while a check runs).
export function NotificationBar({
  tone = 'info',
  title,
  icon,
  children,
  action,
  className,
}: {
  tone?: NotificationTone
  title?: ReactNode
  /** Override the tone's default icon. */
  icon?: LucideIcon
  children?: ReactNode
  /** Trailing slot, vertically centered — a button or link. */
  action?: ReactNode
  className?: string
}) {
  const { Icon: ToneIcon, container, accent } = TONES[tone]
  const Icon = icon ?? ToneIcon
  return (
    <m.div
      role="status"
      initial={{ opacity: 0, y: RISE }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: -RISE }}
      transition={transition.base}
      className={cn(
        'flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-xs',
        container,
        className,
      )}
    >
      <Icon className={cn('mt-px size-4 shrink-0', accent)} aria-hidden />
      <div className="min-w-0 flex-1 leading-relaxed">
        {title ? <p className={cn('font-medium', accent)}>{title}</p> : null}
        {children ? (
          <div className={cn('text-foreground/80', title && 'mt-0.5')}>{children}</div>
        ) : null}
      </div>
      {action ? <div className="shrink-0 self-center">{action}</div> : null}
    </m.div>
  )
}
