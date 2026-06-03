import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { Separator } from '@/components/ui/separator'
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
        <NotificationsForm key={JSON.stringify(data.events)} settings={data} />
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

  const test = useMutation({
    mutationFn: () => notificationsApi.test(),
    onSuccess: (r) => toast.success(t('notifications.toast.tested', { count: r.sent })),
    onError: (e) => toast.error(e.message),
  })

  const save = () =>
    update.mutate(
      {
        // Empty input = leave unchanged (undefined); a value sets it.
        discord_url: discord.trim() || undefined,
        slack_url: slack.trim() || undefined,
        events,
      },
      {
        onSuccess: () => toast.success(t('notifications.toast.saved')),
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

      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium">{t('notifications.events')}</span>
        {Object.keys(events).map((key) => (
          <label key={key} className="flex items-center justify-between gap-2 text-sm">
            <span className="text-muted-foreground">{humanize(key)}</span>
            <Switch
              checked={events[key]}
              onCheckedChange={(v) => setEvents((e) => ({ ...e, [key]: v }))}
            />
          </label>
        ))}
      </div>

      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
          {t('notifications.sendTest')}
        </Button>
        <Button variant="brand" size="sm" disabled={update.isPending} onClick={save}>
          {t('notifications.save')}
        </Button>
      </div>
    </Card>
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
