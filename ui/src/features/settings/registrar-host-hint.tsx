import { Trans, useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/common/copy-button'
import { relativeHost } from '@/features/settings/dns-host'

/**
 * The note under a DNS record telling the operator that most registrars want
 * the record Name/Host *relative* to their domain — entering the full FQDN is
 * the most common reason a freshly-added record silently doesn't resolve.
 */
export function RegistrarHostHint({ hostname }: { hostname: string }) {
  const { t } = useTranslation('settings')
  const relative = relativeHost(hostname)

  return (
    <p className="flex flex-wrap items-center gap-1 text-2xs text-muted-foreground">
      <Trans
        t={t}
        i18nKey="domains.dnsRecord.registrarHint"
        values={{ relative }}
        components={[
          <span className="rounded bg-surface-2 px-1 py-0.5 font-mono text-foreground" />,
        ]}
      />
      <CopyButton value={relative} label="" />
    </p>
  )
}
