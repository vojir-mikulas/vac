import { CheckCircle2, RotateCw } from 'lucide-react'
import { Trans, useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/common/copy-button'
import { DomainCertPanel } from '@/features/settings/domain-cert-panel'
import { RegistrarHostHint } from '@/features/settings/registrar-host-hint'
import { Button } from '@/components/ui/button'
import { useHostStats } from '@/lib/api/metrics'
import { useBaseDomain } from '@/lib/api/instance'
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
  const { data: host } = useHostStats()
  const { data: base } = useBaseDomain()
  const refresh = useRefreshDomainStatus()
  const ip = host?.host_ip || ''
  const baseHost = base?.effective || ''

  const labels = domain.hostname.split('.').filter(Boolean).length
  const isApex = labels <= 2
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
          <p className="text-xs text-muted-foreground">
            <Trans
              t={t}
              i18nKey="domains.config.createRecord"
              values={{ hostname: domain.hostname }}
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
              <span className="truncate">{domain.hostname}</span>
              <span className="flex items-center gap-2">
                <span className="truncate">{ip || '—'}</span>
                {ip ? <CopyButton value={ip} label="" /> : null}
              </span>
            </div>
          </div>

          <RegistrarHostHint hostname={domain.hostname} />

          {!isApex && baseHost ? (
            <p className="text-2xs text-muted-foreground">
              <Trans
                t={t}
                i18nKey="domains.config.cnameAlt"
                values={{ hostname: domain.hostname, baseHost }}
                components={[<span className="font-mono" />]}
              />
            </p>
          ) : null}

          {domain.status_detail ? (
            <p className="text-2xs text-warn-foreground">{domain.status_detail}</p>
          ) : null}

          <p className="text-2xs text-muted-foreground">{t('domains.config.propagation')}</p>
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
