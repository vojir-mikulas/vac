import { CheckCircle2, AlertTriangle, Loader2, XCircle, Clock } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import type { DomainStatusState } from '@/types/api'

/**
 * Single source of truth for how a domain's live DNS/cert status (plan 09 F3)
 * renders. `checking` is a neutral spinner, not an error.
 */
const STATUS: Record<
  DomainStatusState,
  { label: string; variant: 'success' | 'secondary' | 'destructive' | 'outline'; spin?: boolean }
> = {
  checking: { label: 'Checking', variant: 'secondary', spin: true },
  awaiting_dns: { label: 'Awaiting DNS', variant: 'outline' },
  misconfigured: { label: 'Misconfigured', variant: 'destructive' },
  issuing: { label: 'Issuing cert', variant: 'secondary', spin: true },
  active: { label: 'Valid', variant: 'success' },
  error: { label: 'Error', variant: 'destructive' },
}

const ICON: Record<DomainStatusState, typeof CheckCircle2> = {
  checking: Loader2,
  awaiting_dns: Clock,
  misconfigured: AlertTriangle,
  issuing: Loader2,
  active: CheckCircle2,
  error: XCircle,
}

export function DomainStatusBadge({ status }: { status?: DomainStatusState }) {
  const state = status ?? 'checking'
  const cfg = STATUS[state]
  const Icon = ICON[state]
  return (
    <Badge variant={cfg.variant} className="gap-1">
      <Icon className={cfg.spin ? 'animate-spin' : undefined} />
      {cfg.label}
    </Badge>
  )
}
