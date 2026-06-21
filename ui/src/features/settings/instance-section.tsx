import { useTranslation } from 'react-i18next'
import { ExternalLink } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { CopyButton } from '@/components/common/copy-button'
import { cn } from '@/lib/utils'
import { useInstanceInfo, useUpdateCheck, useExportBundle } from '@/lib/api/instance'
import { downloadBlob } from '@/lib/log-export'
import { SUPPORTED_LANGUAGES, changeLanguage, type SupportedLanguage } from '@/i18n'

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

          <UpdateRow />

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

      <MigrationSection />

      <LanguageSection />
    </section>
  )
}

// MigrationSection downloads a portable instance bundle (control DB + every
// encrypted secret + the master key) to move this VAC to another host. It is the
// browser half of `vac migrate`: app *data* volumes are deliberately not included
// (they're bulk and belong to the host CLI), so the card says so. The bundle
// holds secrets, so the action is behind a warning dialog and fresh-2FA step-up
// (enforced transparently by the API client on the POST).
function MigrationSection() {
  const { t } = useTranslation('settings')
  const exportBundle = useExportBundle()

  const handleExport = () =>
    exportBundle.mutate(undefined, {
      onSuccess: (blob) => {
        downloadBlob('vac-instance-bundle.tar', blob)
        toast.success(t('instance.migration.success'))
      },
      onError: (e) => toast.error(e instanceof Error ? e.message : t('instance.migration.error')),
    })

  return (
    <div>
      <SectionHeader>{t('instance.migration.heading')}</SectionHeader>
      <Card className="gap-0 p-0">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div className="max-w-xl">
            <div className="text-sm font-medium">{t('instance.migration.label')}</div>
            <p className="text-xs text-muted-foreground">{t('instance.migration.description')}</p>
          </div>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" size="sm" disabled={exportBundle.isPending}>
                {exportBundle.isPending
                  ? t('instance.migration.exporting')
                  : t('instance.migration.button')}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('instance.migration.confirmTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('instance.migration.confirmDescription')}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('instance.migration.cancel')}</AlertDialogCancel>
                <AlertDialogAction onClick={handleExport}>
                  {t('instance.migration.confirmAction')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </Card>
    </div>
  )
}

// UpdateRow shows whether a newer release exists. When one does, it expands with
// the `vac upgrade` command to run on the host (upgrades happen out-of-band — the
// API can't recreate its own container mid-request) plus a release-notes link.
function UpdateRow() {
  const { t } = useTranslation('settings')
  const { data, isLoading } = useUpdateCheck()

  if (isLoading) {
    return (
      <Row label={t('instance.update.label')}>
        <span className="text-xs text-muted-foreground">{t('instance.update.checking')}</span>
      </Row>
    )
  }

  // A failed upstream check (no data or an error with no version) degrades to a
  // muted "couldn't check" rather than an alarming state.
  if (!data || (data.error && !data.latest)) {
    return (
      <Row label={t('instance.update.label')}>
        <span className="text-xs text-muted-foreground">{t('instance.update.failed')}</span>
      </Row>
    )
  }

  if (!data.update_available) {
    return (
      <Row
        label={t('instance.update.label')}
        hint={data.latest ? t('instance.update.latest', { version: data.latest }) : undefined}
      >
        <Badge variant="success">{t('instance.update.upToDate')}</Badge>
      </Row>
    )
  }

  const command = `vac upgrade ${data.latest}`
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-warn-bg bg-warn-bg/30 p-4">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            {t('instance.update.available')}
            <Badge variant="warn">{data.latest}</Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{t('instance.update.instructions')}</p>
        </div>
        {data.release_url ? (
          <a
            href={data.release_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-info-foreground hover:underline"
          >
            {t('instance.update.releaseNotes')}
            <ExternalLink className="size-3" />
          </a>
        ) : null}
      </div>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded bg-surface-1 px-2.5 py-1.5 font-mono text-xs">
          {command}
        </code>
        <CopyButton value={command} label={t('instance.update.copyCommand')} />
      </div>
    </div>
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
            onValueChange={(lng) => void changeLanguage(lng as SupportedLanguage)}
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
