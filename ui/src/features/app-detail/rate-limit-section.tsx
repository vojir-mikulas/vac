import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SectionHeader } from '@/components/common/section-header'
import { useRateLimit, useSetRateLimit } from '@/lib/api/ratelimit'

// Per-app edge rate limit (docs/plans). The operator caps requests/min/IP; Caddy
// enforces it and returns 429 past the cap. Blank clears the limit. Saving
// re-syncs the proxy on the backend so the change takes effect immediately.
export function RateLimitSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: state } = useRateLimit(appId)
  const setLimit = useSetRateLimit(appId)

  // Draft overlay over the server value (null = no unsaved edit).
  const [draft, setDraft] = useState<string | null>(null)
  const value = draft ?? (state?.rpm != null ? String(state.rpm) : '')

  const save = () => {
    const trimmed = value.trim()
    // Blank clears the limit.
    const rpm = trimmed === '' ? null : Number(trimmed)
    if (rpm !== null && (!Number.isInteger(rpm) || rpm <= 0)) {
      toast.error(t('rateLimit.invalid'))
      return
    }
    setLimit.mutate(rpm, {
      onSuccess: () => {
        toast.success(rpm === null ? t('rateLimit.cleared') : t('rateLimit.updated'))
        setDraft(null)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <section>
      <SectionHeader>{t('rateLimit.title')}</SectionHeader>
      <Card className="gap-4 p-5">
        <p className="text-xs text-muted-foreground">{t('rateLimit.intro')}</p>
        <div className="grid gap-2">
          <Label htmlFor="rate-limit">{t('rateLimit.label')}</Label>
          <Input
            id="rate-limit"
            inputMode="numeric"
            placeholder={t('rateLimit.placeholder')}
            value={value}
            onChange={(e) => setDraft(e.target.value)}
            className="max-w-40 font-mono text-xs"
          />
          <p className="text-xs text-muted-foreground">{t('rateLimit.hint')}</p>
        </div>
        <div className="flex justify-end">
          <Button
            variant="brand"
            size="sm"
            disabled={setLimit.isPending || draft === null}
            onClick={save}
          >
            {t('rateLimit.save')}
          </Button>
        </div>
      </Card>
    </section>
  )
}
