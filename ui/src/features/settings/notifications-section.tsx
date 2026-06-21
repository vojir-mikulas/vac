import { type ReactNode, useId, useState } from 'react'
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
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { Separator } from '@/components/ui/separator'
import { Textarea } from '@/components/ui/textarea'
import {
  notificationsApi,
  useNotificationSettings,
  useUpdateNotifications,
} from '@/lib/api/notifications'
import { useMutation } from '@tanstack/react-query'
import type { NotificationEvents, NotificationSettings } from '@/types/api'

function humanize(key: string): string {
  const s = key.replace(/_/g, ' ')
  return s.charAt(0).toUpperCase() + s.slice(1)
}

export function NotificationsSection() {
  const { t } = useTranslation('settings')
  const { data, isLoading } = useNotificationSettings()
  return (
    <section>
      <SectionHeader>{t('notifications.heading')}</SectionHeader>
      {isLoading || !data ? (
        <Card className="p-5">
          <Skeleton className="h-40 w-full" />
        </Card>
      ) : (
        <NotificationsForm settings={data} />
      )}
    </section>
  )
}

function NotificationsForm({ settings }: { settings: NotificationSettings }) {
  const { t } = useTranslation('settings')
  const update = useUpdateNotifications()
  const [discord, setDiscord] = useState('')
  const [slack, setSlack] = useState('')
  const [events, setEvents] = useState<NotificationEvents>(settings.events)

  // SMTP config fields are returned plaintext, so they seed the inputs directly;
  // only the password is write-only (blank input, redacted hint placeholder).
  const [smtpHost, setSmtpHost] = useState(settings.smtp_host)
  const [smtpPort, setSmtpPort] = useState(settings.smtp_port ? String(settings.smtp_port) : '')
  const [smtpUsername, setSmtpUsername] = useState(settings.smtp_username)
  const [smtpPassword, setSmtpPassword] = useState('')
  const [smtpFrom, setSmtpFrom] = useState(settings.smtp_from)
  const [smtpTo, setSmtpTo] = useState(settings.smtp_to)
  const [smtpTlsMode, setSmtpTlsMode] = useState(settings.smtp_tls_mode || 'starttls')

  // Re-seed the toggles when the server value changes (a refetch yields a new
  // reference via structural sharing) without remounting the form — that would
  // discard the operator's unsaved Discord/Slack webhook input. Mirrors the
  // "track the source in state" pattern in env-tab.
  const [seeded, setSeeded] = useState(settings.events)
  if (settings.events !== seeded) {
    setSeeded(settings.events)
    setEvents(settings.events)
  }

  const test = useMutation({
    mutationFn: () => notificationsApi.test(),
    onSuccess: (r) => toast.success(t('notifications.toast.tested', { count: r.sent })),
    onError: (e) => toast.error(e.message),
  })

  // Has anything been edited vs. the saved settings? Webhook + password inputs
  // start blank (blank = "leave unchanged"), so any text in them counts as dirty.
  // "Send test" hits the *saved* config, so we block it while dirty to avoid
  // testing stale settings; Save is disabled when there's nothing to save.
  // Port must be empty (channel off) or a valid TCP port — a number input still
  // accepts out-of-range values, which would just fail opaquely at send time.
  const smtpPortValid =
    smtpPort.trim() === '' ||
    (Number.isInteger(Number(smtpPort)) && Number(smtpPort) >= 1 && Number(smtpPort) <= 65535)

  const eventsChanged = Object.keys(events).some((k) => events[k] !== settings.events[k])
  const dirty =
    discord.trim() !== '' ||
    slack.trim() !== '' ||
    smtpPassword.trim() !== '' ||
    smtpHost !== settings.smtp_host ||
    (smtpPort.trim() ? Number(smtpPort) : null) !== (settings.smtp_port ?? null) ||
    smtpUsername !== settings.smtp_username ||
    smtpFrom !== settings.smtp_from ||
    smtpTo !== settings.smtp_to ||
    smtpTlsMode !== (settings.smtp_tls_mode || 'starttls') ||
    eventsChanged

  const save = () =>
    update.mutate(
      {
        // Webhook URLs: empty input = leave unchanged (undefined); a value sets it.
        discord_url: discord.trim() || undefined,
        slack_url: slack.trim() || undefined,
        // SMTP config is sent as-is (empty string clears, turning the channel off);
        // the password is write-only, so blank = leave unchanged.
        smtp_host: smtpHost.trim(),
        smtp_port: smtpPort.trim() ? Number(smtpPort) : null,
        smtp_username: smtpUsername.trim(),
        smtp_password: smtpPassword.trim() || undefined,
        smtp_from: smtpFrom.trim(),
        smtp_to: smtpTo,
        smtp_tls_mode: smtpTlsMode,
        events,
      },
      {
        onSuccess: () => {
          setSmtpPassword('')
          toast.success(t('notifications.toast.saved'))
        },
        onError: (e) => toast.error(e.message),
      },
    )

  return (
    <Card className="gap-5 p-5">
      <ChannelField
        label={t('notifications.discordWebhook')}
        configured={settings.discord_configured}
        hint={settings.discord_hint}
        value={discord}
        onChange={setDiscord}
      />
      <ChannelField
        label={t('notifications.slackWebhook')}
        configured={settings.slack_configured}
        hint={settings.slack_hint}
        value={slack}
        onChange={setSlack}
      />

      <Separator />

      <div className="flex flex-col gap-4">
        <span className="text-sm font-medium">{t('notifications.email.heading')}</span>
        <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
          <Field label={t('notifications.email.host')}>
            <Input
              value={smtpHost}
              onChange={(e) => setSmtpHost(e.target.value)}
              placeholder="smtp.example.com"
              className="font-mono text-xs"
            />
          </Field>
          <Field label={t('notifications.email.port')}>
            <Input
              type="number"
              value={smtpPort}
              onChange={(e) => setSmtpPort(e.target.value)}
              placeholder="587"
              aria-invalid={!smtpPortValid}
              className="font-mono text-xs"
            />
            {!smtpPortValid ? (
              <p className="text-2xs text-err-foreground">{t('notifications.email.portInvalid')}</p>
            ) : null}
          </Field>
        </div>
        <Field label={t('notifications.email.username')}>
          <Input
            value={smtpUsername}
            onChange={(e) => setSmtpUsername(e.target.value)}
            placeholder={t('notifications.email.usernamePlaceholder')}
            className="font-mono text-xs"
            autoComplete="off"
          />
        </Field>
        <Field label={t('notifications.email.password')}>
          <Input
            type="password"
            value={smtpPassword}
            onChange={(e) => setSmtpPassword(e.target.value)}
            placeholder={
              settings.smtp_password_configured
                ? t('notifications.configuredPlaceholder', { hint: settings.smtp_password_hint })
                : '••••••••'
            }
            className="font-mono text-xs"
            autoComplete="new-password"
          />
        </Field>
        <Field label={t('notifications.email.from')}>
          <Input
            value={smtpFrom}
            onChange={(e) => setSmtpFrom(e.target.value)}
            placeholder="vac@example.com"
            className="font-mono text-xs"
          />
        </Field>
        <Field label={t('notifications.email.to')} hint={t('notifications.email.toHint')}>
          <Textarea
            value={smtpTo}
            onChange={(e) => setSmtpTo(e.target.value)}
            placeholder="ops@example.com, alerts@example.com"
            className="font-mono text-xs"
            rows={2}
          />
        </Field>
        <Field label={t('notifications.email.tlsMode')}>
          <Select value={smtpTlsMode} onValueChange={setSmtpTlsMode}>
            <SelectTrigger className="w-44" aria-label={t('notifications.email.tlsMode')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="starttls">{t('notifications.email.tls.starttls')}</SelectItem>
              <SelectItem value="implicit">{t('notifications.email.tls.implicit')}</SelectItem>
              <SelectItem value="none">{t('notifications.email.tls.none')}</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>

      <Separator />

      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium">{t('notifications.events')}</span>
        {Object.keys(events).map((key) => (
          <label key={key} className="flex items-center justify-between gap-2 text-sm">
            <span className="text-muted-foreground">
              {t(`notifications.eventLabels.${key}`, { defaultValue: humanize(key) })}
            </span>
            <Switch
              checked={events[key]}
              onCheckedChange={(v) => setEvents((e) => ({ ...e, [key]: v }))}
            />
          </label>
        ))}
      </div>

      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={test.isPending || dirty}
          title={dirty ? t('notifications.testDirtyHint') : undefined}
          onClick={() => test.mutate()}
        >
          {t('notifications.sendTest')}
        </Button>
        <Button
          variant="brand"
          size="sm"
          disabled={update.isPending || !dirty || !smtpPortValid}
          onClick={save}
        >
          {t('notifications.save')}
        </Button>
      </div>
    </Card>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="grid gap-2">
      <Label>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

function ChannelField({
  label,
  configured,
  hint,
  value,
  onChange,
}: {
  label: string
  configured: boolean
  hint: string
  value: string
  onChange: (v: string) => void
}) {
  const { t } = useTranslation('settings')
  const id = useId()
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="url"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={configured ? t('notifications.configuredPlaceholder', { hint }) : 'https://…'}
        className="font-mono text-xs"
      />
    </div>
  )
}
