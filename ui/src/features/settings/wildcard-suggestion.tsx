import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Trans, useTranslation } from 'react-i18next'
import { CheckCircle2, ChevronDown, Loader2 } from 'lucide-react'

import { CopyButton } from '@/components/common/copy-button'
import { RegistrarHostHint } from '@/features/settings/registrar-host-hint'
import { Button } from '@/components/ui/button'
import { useHostStats } from '@/lib/api/metrics'
import { useDnsCheck } from '@/lib/api/instance'
import { queryKeys } from '@/lib/query/keys'
import { cn } from '@/lib/utils'

/**
 * Guides the operator to add the wildcard record automatic subdomains need
 * (plan 09 F2). A bare `*` can't be resolved, so we probe a throwaway label
 * under the base domain — if it resolves here, the wildcard is live. The probe
 * runs automatically (cached) so the happy path collapses to a one-line
 * confirmation and only an unconfigured wildcard expands into the setup record.
 */
export function WildcardSuggestion({ baseDomain }: { baseDomain: string }) {
  const { t } = useTranslation('settings')
  const qc = useQueryClient()
  const { data: host } = useHostStats()
  const ip = host?.host_ip || ''
  const probeHost = `_vac-wildcard-check.${baseDomain}`

  const check = useDnsCheck(probeHost)
  const ok = check.data?.points_here === true
  const [expanded, setExpanded] = useState(false)

  const recheck = () => qc.invalidateQueries({ queryKey: queryKeys.instance.dnsCheck(probeHost) })

  // Resolved & pointing here: collapse to a quiet, reassuring confirmation.
  if (ok) {
    return (
      <div className="flex items-center gap-2 rounded-md border border-ok-border bg-ok-bg/40 px-3 py-2 text-xs text-ok-foreground">
        <CheckCircle2 className="size-4 shrink-0" />
        <span className="flex-1">{t('domains.wildcard.ok')}</span>
      </div>
    )
  }

  // Still probing on first load — keep it to a single muted line.
  if (check.isLoading) {
    return (
      <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" />
        {t('domains.wildcard.checkingStatus')}
      </div>
    )
  }

  // Not configured yet: a compact header that expands into the DNS record.
  return (
    <div className="rounded-md border bg-surface-1 text-sm">
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs"
      >
        <span className="flex-1 text-muted-foreground">
          <Trans
            t={t}
            i18nKey="domains.wildcard.intro"
            values={{ baseDomain }}
            components={[<span className="font-mono text-foreground" />]}
          />
        </span>
        <ChevronDown
          className={cn('size-4 shrink-0 transition-transform', expanded && 'rotate-180')}
        />
      </button>

      {expanded ? (
        <div className="flex flex-col gap-3 border-t px-3 py-3">
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

          <RegistrarHostHint hostname={`*.${baseDomain}`} />

          <div className="flex items-center gap-3">
            <Button variant="outline" size="sm" disabled={check.isFetching} onClick={recheck}>
              {check.isFetching ? t('domains.wildcard.checking') : t('domains.wildcard.recheck')}
            </Button>
            {check.data && !check.isFetching ? (
              <span className="text-xs text-warn-foreground">
                {t('domains.wildcard.notResolving')}
              </span>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  )
}
