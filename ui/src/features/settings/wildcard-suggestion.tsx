import { useMutation } from '@tanstack/react-query'
import { Trans, useTranslation } from 'react-i18next'
import { CheckCircle2, AlertTriangle } from 'lucide-react'

import { CopyButton } from '@/components/common/copy-button'
import { Button } from '@/components/ui/button'
import { useHostStats } from '@/lib/api/metrics'
import { instanceApi } from '@/lib/api/instance'

/**
 * Guides the operator to add the wildcard record automatic subdomains need
 * (plan 09 F2). A bare `*` can't be resolved, so the Check probes a throwaway
 * label under the base domain — if it resolves here, the wildcard is live.
 */
export function WildcardSuggestion({ baseDomain }: { baseDomain: string }) {
  const { t } = useTranslation('settings')
  const { data: host } = useHostStats()
  const ip = host?.host_ip || ''
  const probeHost = `_vac-wildcard-check.${baseDomain}`

  const check = useMutation({ mutationFn: () => instanceApi.dnsCheck(probeHost) })

  return (
    <div className="flex flex-col gap-3 rounded-md border bg-surface-1 p-3 text-sm">
      <p className="text-xs text-muted-foreground">
        <Trans
          t={t}
          i18nKey="domains.wildcard.intro"
          values={{ baseDomain }}
          components={[<span className="font-mono" />]}
        />
      </p>

      <div className="overflow-hidden rounded-md border">
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b bg-surface-2 px-3 py-1.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
          <span>{t('domains.dnsRecord.type')}</span>
          <span>{t('domains.dnsRecord.name')}</span>
          <span>{t('domains.dnsRecord.value')}</span>
        </div>
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 py-2 font-mono text-xs">
          <span className="rounded bg-surface-2 px-1.5 py-0.5">A</span>
          <span className="truncate">*.{baseDomain}</span>
          <span className="flex items-center gap-2">
            <span className="truncate">{ip || '—'}</span>
            {ip ? <CopyButton value={ip} label="" /> : null}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={check.isPending}
          onClick={() => check.mutate()}
        >
          {check.isPending ? t('domains.wildcard.checking') : t('domains.wildcard.check')}
        </Button>
        {check.data ? (
          check.data.points_here ? (
            <span className="flex items-center gap-1.5 text-xs text-ok-foreground">
              <CheckCircle2 className="size-4" />
              {t('domains.wildcard.pointsHere')}
            </span>
          ) : (
            <span className="flex items-center gap-1.5 text-xs text-warn-foreground">
              <AlertTriangle className="size-4" />
              {t('domains.wildcard.notResolving')}
            </span>
          )
        ) : null}
      </div>
    </div>
  )
}
