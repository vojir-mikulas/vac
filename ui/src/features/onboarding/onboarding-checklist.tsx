import { Link } from '@tanstack/react-router'
import { CheckCircle2, Circle, Rocket, Settings as SettingsIcon, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { CopyButton } from '@/components/common/copy-button'
import { useApps } from '@/lib/api/apps'
import { useBaseDomain, useDismissOnboarding, useOnboarding } from '@/lib/api/instance'
import { useHostStats } from '@/lib/api/metrics'

// OnboardingChecklist is the dismissible first-run guide (plan 04). It is
// non-blocking by design — the operator can ignore or dismiss it — and each step
// reflects real instance state rather than a stored wizard cursor, so it stays
// honest if the operator does the work out of order or from the CLI.
export function OnboardingChecklist() {
  const { t } = useTranslation('onboarding')
  const { data: onboarding } = useOnboarding()
  const { data: apps } = useApps()
  const { data: baseDomain } = useBaseDomain()
  const { data: host } = useHostStats()
  const dismiss = useDismissOnboarding()

  const hasBaseDomain = Boolean(baseDomain?.effective)
  const hasApp = (apps?.length ?? 0) > 0
  const done = [hasBaseDomain, hasApp].filter(Boolean).length

  // Hide once dismissed, fully complete, or before state has loaded. Don't flash
  // the card for an established instance while queries are still resolving.
  if (onboarding?.dismissed || done === 2 || !onboarding || !apps || !baseDomain) {
    return null
  }

  return (
    <Card className="mb-6 gap-4 border-brand/30 bg-brand/[0.03] p-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold">{t('heading')}</h2>
          <p className="text-xs text-muted-foreground">{t('subheading', { done, total: 2 })}</p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground"
          aria-label={t('dismiss')}
          disabled={dismiss.isPending}
          onClick={() => dismiss.mutate()}
        >
          <X className="size-4" />
        </Button>
      </div>

      <ol className="flex flex-col gap-3">
        <Step
          done={hasBaseDomain}
          title={t('steps.baseDomain.title')}
          description={t('steps.baseDomain.description')}
          action={
            <Button variant="outline" size="sm" asChild>
              <Link to="/settings">
                <SettingsIcon className="size-3.5" />
                {t('steps.baseDomain.action')}
              </Link>
            </Button>
          }
        >
          {host?.host_ip ? (
            <p className="mt-1 flex flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
              {t('steps.baseDomain.aRecordHint')}
              <code className="rounded bg-surface-2 px-1 py-0.5 font-mono">{host.host_ip}</code>
              <CopyButton value={host.host_ip} label={t('steps.baseDomain.copyIp')} />
            </p>
          ) : null}
        </Step>

        <Step
          done={hasApp}
          title={t('steps.firstApp.title')}
          description={t('steps.firstApp.description')}
          action={
            <Button variant="brand" size="sm" asChild>
              <Link to="/apps/new">
                <Rocket className="size-3.5" />
                {t('steps.firstApp.action')}
              </Link>
            </Button>
          }
        />
      </ol>
    </Card>
  )
}

function Step({
  done,
  title,
  description,
  action,
  children,
}: {
  done: boolean
  title: string
  description: string
  action: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <li className="flex items-start gap-3">
      {done ? (
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" />
      ) : (
        <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      )}
      <div className="min-w-0 flex-1">
        <div className={`text-sm font-medium ${done ? 'text-muted-foreground line-through' : ''}`}>
          {title}
        </div>
        <div className="text-xs text-muted-foreground">{description}</div>
        {children}
      </div>
      {done ? null : <div className="shrink-0">{action}</div>}
    </li>
  )
}
