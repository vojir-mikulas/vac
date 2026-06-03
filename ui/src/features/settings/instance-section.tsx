import { useTranslation } from 'react-i18next'

import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { useInstanceInfo } from '@/lib/api/instance'
import { SUPPORTED_LANGUAGES } from '@/i18n'

const CHANNELS = ['stable', 'beta', 'edge'] as const

export function InstanceSection() {
  const { t } = useTranslation('settings')
  const { data, isLoading } = useInstanceInfo()

  return (
    <section className="flex flex-col gap-8">
      <div>
        <SectionHeader>{t('instance.version.heading')}</SectionHeader>
        <Card className="gap-5 p-5">
          <Row
            label={t('instance.version.current')}
            hint={
              data?.built_at
                ? t('instance.version.builtAt', { date: formatBuilt(data.built_at) })
                : undefined
            }
          >
            {isLoading ? (
              <Skeleton className="h-5 w-24" />
            ) : (
              <span className="font-mono text-sm">
                {t('instance.version.value', { version: data?.version || 'dev' })}
              </span>
            )}
          </Row>

          <Row label={t('instance.channel.label')} hint={t('instance.channel.hint')}>
            <div className="inline-flex items-center gap-0.5 rounded-md border bg-surface-1 p-0.5 opacity-60">
              {CHANNELS.map((c) => (
                <span
                  key={c}
                  className={cn(
                    'rounded px-2.5 py-1 text-xs font-medium capitalize',
                    c === (data?.channel ?? 'stable')
                      ? 'bg-surface-2 text-foreground'
                      : 'text-muted-foreground',
                  )}
                >
                  {c}
                </span>
              ))}
            </div>
          </Row>

          <Row label={t('instance.autoUpdate.label')} hint={t('instance.autoUpdate.hint')}>
            <div className="flex items-center gap-2">
              <Badge variant="secondary">{t('instance.comingSoon')}</Badge>
              <Switch checked={false} disabled aria-label={t('instance.autoUpdate.ariaLabel')} />
            </div>
          </Row>
        </Card>
      </div>

      <LanguageSection />
    </section>
  )
}

function LanguageSection() {
  const { t, i18n } = useTranslation('settings')

  return (
    <div>
      <SectionHeader>{t('language.heading')}</SectionHeader>
      <Card className="gap-5 p-5">
        <Row label={t('language.label')} hint={t('language.hint')}>
          <Select
            value={i18n.resolvedLanguage}
            onValueChange={(lng) => void i18n.changeLanguage(lng)}
          >
            <SelectTrigger className="w-40" aria-label={t('language.label')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SUPPORTED_LANGUAGES.map((lang) => (
                <SelectItem key={lang.code} value={lang.code}>
                  {lang.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      </Card>
    </div>
  )
}

function Row({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-sm font-medium">{label}</div>
        {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function formatBuilt(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}
