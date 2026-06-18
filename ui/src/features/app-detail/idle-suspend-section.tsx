import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { SectionHeader } from '@/components/common/section-header'
import { useIdleSuspend, useSetIdleSuspend } from '@/lib/api/scaletozero'

// Scale-to-zero per-app section (docs/plans/scale-to-zero.md). Rendered only when
// the instance master flag is on (settings-tab gates it). Lets the operator opt
// the app into idle-suspend and override the inactivity window.
export function IdleSuspendSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: state } = useIdleSuspend(appId)
  const setIdle = useSetIdleSuspend(appId)

  const enabled = state?.enabled ?? false
  // Draft overlay over the server value (null = no unsaved edit).
  const [draft, setDraft] = useState<string | null>(null)
  const timeoutValue =
    draft ?? (state?.timeout_minutes != null ? String(state.timeout_minutes) : '')

  const save = (next: { enabled?: boolean; timeout?: number | null }) =>
    setIdle.mutate(
      {
        enabled: next.enabled ?? enabled,
        timeout_minutes:
          next.timeout !== undefined ? next.timeout : (state?.timeout_minutes ?? null),
      },
      { onError: (e) => toast.error(e.message) },
    )

  const saveTimeout = () => {
    const trimmed = timeoutValue.trim()
    // Blank clears the override → the instance default applies.
    const value = trimmed === '' ? null : Number(trimmed)
    if (value !== null && (!Number.isInteger(value) || value <= 0)) {
      toast.error(t('idleSuspend.timeoutInvalid'))
      return
    }
    save({ timeout: value })
    setDraft(null)
  }

  return (
    <section>
      <SectionHeader>{t('idleSuspend.title')}</SectionHeader>
      <Card className="gap-5 p-5">
        <p className="text-xs text-muted-foreground">{t('idleSuspend.intro')}</p>

        <div className="flex items-center justify-between gap-4">
          <div className="grid gap-0.5">
            <div className="flex items-center gap-2">
              <Label htmlFor="idle-enabled">{t('idleSuspend.enable')}</Label>
              {state?.suspended ? (
                <Badge variant="secondary" className="uppercase">
                  {t('idleSuspend.suspendedBadge')}
                </Badge>
              ) : null}
            </div>
            <p className="text-xs text-muted-foreground">{t('idleSuspend.enableHint')}</p>
          </div>
          <Switch
            id="idle-enabled"
            checked={enabled}
            disabled={setIdle.isPending}
            onCheckedChange={(v) => save({ enabled: v })}
          />
        </div>

        {enabled ? (
          <div className="grid gap-2">
            <Label htmlFor="idle-timeout">{t('idleSuspend.timeout')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="idle-timeout"
                inputMode="numeric"
                placeholder={t('idleSuspend.timeoutPlaceholder')}
                value={timeoutValue}
                onChange={(e) => setDraft(e.target.value)}
                className="max-w-40 font-mono text-xs"
              />
              <Button
                variant="outline"
                size="sm"
                disabled={setIdle.isPending || draft === null}
                onClick={saveTimeout}
              >
                {t('idleSuspend.save')}
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">{t('idleSuspend.timeoutHint')}</p>
          </div>
        ) : null}
      </Card>
    </section>
  )
}
