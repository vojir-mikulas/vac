import { CheckCircle2, AlertTriangle, Loader2, XCircle, Clock } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import type { DomainStatusState } from '@/types/api'

/**
 * Single source of truth for how a domain's live DNS/cert status (plan 09 F3)
 * renders. `checking` is a neutral spinner, not an error.
 */
const STATUS = {
  checking: { variant: 'secondary', spin: true },
  awaiting_dns: { variant: 'outline' },
  misconfigured: { variant: 'destructive' },
  issuing: { variant: 'secondary', spin: true },
  active: { variant: 'success' },
  error: { variant: 'destructive' },
} satisfies Record<
  DomainStatusState,
  { variant: 'success' | 'secondary' | 'destructive' | 'outline'; spin?: boolean }
>

const ICON: Record<DomainStatusState, typeof CheckCircle2> = {
  checking: Loader2,
  awaiting_dns: Clock,
  misconfigured: AlertTriangle,
  issuing: Loader2,
  active: CheckCircle2,
  error: XCircle,
}

export function DomainStatusBadge({ status }: { status?: DomainStatusState }) {
  const { t } = useTranslation('settings')
  const state = status ?? 'checking'
  const cfg = STATUS[state]
  const Icon = ICON[state]
  return (
    <Badge variant={cfg.variant} className="gap-1">
      <Icon className={'spin' in cfg && cfg.spin ? 'animate-spin' : undefined} />
      {t(`domains.status.${state}`)}
    </Badge>
  )
}
