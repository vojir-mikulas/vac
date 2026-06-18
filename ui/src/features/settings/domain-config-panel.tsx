import { CheckCircle2, RotateCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DomainCertPanel } from '@/features/settings/domain-cert-panel'
import { DnsRecordPreview } from '@/features/settings/dns-record-preview'
import { Button } from '@/components/ui/button'
import { useRefreshDomainStatus } from '@/lib/api/domains'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Domain } from '@/types/api'

/**
 * The Vercel-style "Valid / Invalid Configuration" card for one domain: the
 * exact DNS record to create (real VPS IP, apex-vs-subdomain aware), the live
 * status with its reason, and a manual Refresh that force-probes the host.
 */
export function DomainConfigPanel({ domain }: { domain: Domain }) {
  const { t } = useTranslation('settings')
  const refresh = useRefreshDomainStatus()

  const active = domain.status === 'active'

  return (
    <div className="flex flex-col gap-3 rounded-md border bg-surface-1 p-3 text-sm">
      {active ? (
        <div className="flex items-center gap-2 font-medium text-ok-foreground">
          <CheckCircle2 className="size-4" />
          {t('domains.config.valid')}
          {domain.cert_not_after ? (
            <span className="text-2xs font-normal text-muted-foreground">
              {t('domains.config.certValid')}
            </span>
          ) : null}
        </div>
      ) : (
        <>
          <DnsRecordPreview hostname={domain.hostname} />
          {domain.status_detail ? (
            <p className="text-2xs text-warn-foreground">{domain.status_detail}</p>
          ) : null}
        </>
      )}

      <div className="flex items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={refresh.isPending}
          onClick={() => refresh.mutate(domain.hostname)}
        >
          <RotateCw className={cn('size-3.5', refresh.isPending && 'animate-spin')} />
          {refresh.isPending ? t('domains.config.checking') : t('domains.config.refresh')}
        </Button>
        {domain.last_checked ? (
          <span className="text-2xs text-muted-foreground">
            {t('domains.config.checked', { time: relativeTime(domain.last_checked) })}
          </span>
        ) : null}
      </div>

      {/* Bring-your-own TLS cert (plan B) — custom rows only (auto hosts have no id). */}
      {domain.id && !domain.managed ? <DomainCertPanel domain={domain} /> : null}
    </div>
  )
}
