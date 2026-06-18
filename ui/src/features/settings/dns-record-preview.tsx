import { Trans, useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/common/copy-button'
import { RegistrarHostHint } from '@/features/settings/registrar-host-hint'
import { useHostStats } from '@/lib/api/metrics'
import { useBaseDomain } from '@/lib/api/instance'

/**
 * The DNS guidance for a hostname that isn't serving yet: the exact A record to
 * create (real VPS IP, apex-aware), the registrar-relative host hint, an
 * optional CNAME alternative for subdomains, and the propagation note. Shared by
 * the Domains config panel (for an existing domain) and the new-app wizard's
 * Domain step (for a hostname being typed) so the two never drift.
 */
export function DnsRecordPreview({ hostname }: { hostname: string }) {
  const { t } = useTranslation('settings')
  const { data: host } = useHostStats()
  const { data: base } = useBaseDomain()
  const ip = host?.host_ip || ''
  const baseHost = base?.effective || ''

  const labels = hostname.split('.').filter(Boolean).length
  const isApex = labels <= 2

  return (
    <>
      <p className="text-xs text-muted-foreground">
        <Trans
          t={t}
          i18nKey="domains.config.createRecord"
          values={{ hostname }}
          components={[<span className="font-mono" />]}
        />
        {isApex ? ` ${t('domains.config.apexNote')}` : ''}
      </p>

      <div className="overflow-hidden rounded-md border">
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b bg-surface-2 px-3 py-1.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
          <span>{t('domains.dnsRecord.type')}</span>
          <span>{t('domains.dnsRecord.name')}</span>
          <span>{t('domains.dnsRecord.value')}</span>
        </div>
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 py-2 font-mono text-xs">
          <span className="rounded bg-surface-2 px-1.5 py-0.5">A</span>
          <span className="truncate">{hostname}</span>
          <span className="flex items-center gap-2">
            <span className="truncate">{ip || '—'}</span>
            {ip ? <CopyButton value={ip} label="" /> : null}
          </span>
        </div>
      </div>

      <RegistrarHostHint hostname={hostname} />

      {!isApex && baseHost ? (
        <p className="text-2xs text-muted-foreground">
          <Trans
            t={t}
            i18nKey="domains.config.cnameAlt"
            values={{ hostname, baseHost }}
            components={[<span className="font-mono" />]}
          />
        </p>
      ) : null}

      <p className="text-2xs text-muted-foreground">{t('domains.config.propagation')}</p>
    </>
  )
}
