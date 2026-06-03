import { AlertTriangle, RotateCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// Sibling of EmptyState (same dashed-card visual language): the read-path error
// panel a feature shows when a GET fails. Inlined inside the panel's SwapFade so
// a failed query degrades just that panel — never the whole route — and an
// operator can tell "API is down" from "nothing here yet". onRetry wires to the
// query's refetch.
export function ErrorState({
  title,
  message,
  onRetry,
  className,
}: {
  title?: string
  message?: string
  onRetry?: () => void
  className?: string
}) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'grid place-items-center rounded-xl border border-dashed border-err-border bg-err-bg/40 px-6 py-16 text-center',
        className,
      )}
    >
      <div className="flex max-w-sm flex-col items-center gap-3">
        <AlertTriangle className="size-6 text-err-foreground" />
        <div className="text-sm font-medium">{title ?? t('errorState.title')}</div>
        <p className="text-sm text-muted-foreground">{message ?? t('errorState.description')}</p>
        {onRetry ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCw className="size-3.5" />
            {t('errorState.retry')}
          </Button>
        ) : null}
      </div>
    </div>
  )
}
