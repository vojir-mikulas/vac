import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useDNSSettings, useUpdateDNSSettings } from '@/lib/api/dns'
import type { DNSSettings } from '@/types/api'

const PROVIDER_NONE = 'none'

/**
 * Instance DNS-provider settings (plan A). Renders only when the feature flag
 * (VAC_DNS_AUTOMATION) is on — the GET reports enabled=false otherwise. Saving
 * is step-up gated server-side (handled transparently by the global client).
 */
export function DnsProviderSection() {
  const { data } = useDNSSettings()
  if (!data?.enabled) return null
  // Key by the stored values so the form re-seeds (via useState initializers)
  // after a save invalidates the query — no setState-in-effect needed.
  return <DnsProviderCard key={`${data.provider}|${data.zone}|${data.token_set}`} settings={data} />
}

function DnsProviderCard({ settings }: { settings: DNSSettings }) {
  const { t } = useTranslation('settings')
  const update = useUpdateDNSSettings()
  const zoneId = useId()
  const tokenId = useId()

  const [provider, setProvider] = useState(settings.provider || PROVIDER_NONE)
  const [zone, setZone] = useState(settings.zone || '')
  const [token, setToken] = useState('')

  const off = provider === PROVIDER_NONE
  const tokenSet = settings.token_set

  function save() {
    const body = off
      ? { provider: '', zone: '' }
      : { provider, zone: zone.trim(), token: token.trim() || undefined }
    update.mutate(body, {
      onSuccess: () => {
        toast.success(
          off ? t('domains.dnsProvider.toast.cleared') : t('domains.dnsProvider.toast.saved'),
        )
        setToken('')
      },
    })
  }

  return (
    <section>
      <SectionHeader>{t('domains.dnsProvider.title')}</SectionHeader>
      <Card className="flex flex-col gap-4 p-5">
        <p className="text-sm text-muted-foreground">{t('domains.dnsProvider.description')}</p>

        <div className="flex flex-col gap-1.5">
          <Label>{t('domains.dnsProvider.providerLabel')}</Label>
          <Select value={provider} onValueChange={setProvider}>
            <SelectTrigger className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={PROVIDER_NONE}>{t('domains.dnsProvider.providerNone')}</SelectItem>
              <SelectItem value="cloudflare">
                {t('domains.dnsProvider.providerCloudflare')}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        {!off ? (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={zoneId}>{t('domains.dnsProvider.zoneLabel')}</Label>
              <Input
                id={zoneId}
                className="w-full sm:w-72"
                placeholder={t('domains.dnsProvider.zonePlaceholder')}
                value={zone}
                onChange={(e) => setZone(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={tokenId}>{t('domains.dnsProvider.tokenLabel')}</Label>
              <Input
                id={tokenId}
                type="password"
                autoComplete="off"
                className="w-full sm:w-96"
                placeholder={t('domains.dnsProvider.tokenPlaceholder')}
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <p className="text-2xs text-muted-foreground">
                {tokenSet ? t('domains.dnsProvider.tokenSet') : t('domains.dnsProvider.tokenHelp')}
              </p>
            </div>
            <p className="text-2xs text-muted-foreground">{t('domains.dnsProvider.proxiedNote')}</p>
          </>
        ) : null}

        <div>
          <Button
            size="sm"
            disabled={
              update.isPending || (!off && !zone.trim()) || (!off && !tokenSet && !token.trim())
            }
            onClick={save}
          >
            {update.isPending ? t('domains.dnsProvider.saving') : t('domains.dnsProvider.save')}
          </Button>
        </div>
      </Card>
    </section>
  )
}
