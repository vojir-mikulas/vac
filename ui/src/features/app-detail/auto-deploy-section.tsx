import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import { toast } from 'sonner'

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
import { Badge } from '@/components/ui/badge'
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
import { CopyButton } from '@/components/common/copy-button'
import { SectionHeader } from '@/components/common/section-header'
import {
  useCreateTrigger,
  useDeleteTrigger,
  useDisableWebhook,
  useRegenerateWebhook,
  useTriggers,
  useWebhookConfig,
  type TriggerEvent,
} from '@/lib/api/auto-deploy'

export function AutoDeploySection({
  appId,
  defaultBranch,
}: {
  appId: string
  defaultBranch: string
}) {
  const { t } = useTranslation('app-detail')
  return (
    <section>
      <SectionHeader>{t('autoDeploy.title')}</SectionHeader>
      <Card className="gap-5 p-5">
        <p className="text-xs text-muted-foreground">{t('autoDeploy.intro')}</p>
        <TriggerRules appId={appId} defaultBranch={defaultBranch} />
        <div className="border-t" />
        <WebhookConfigCard appId={appId} />
      </Card>
    </section>
  )
}

function TriggerRules({ appId, defaultBranch }: { appId: string; defaultBranch: string }) {
  const { t } = useTranslation('app-detail')
  const { data: triggers } = useTriggers(appId)
  const create = useCreateTrigger(appId)
  const remove = useDeleteTrigger(appId)

  const [event, setEvent] = useState<TriggerEvent>('push')
  const [filter, setFilter] = useState('')

  const addRule = () =>
    create.mutate(
      { event, filter: filter.trim() },
      {
        onSuccess: () => {
          setFilter('')
          toast.success(t('autoDeploy.triggerAdded'))
        },
        onError: (e) => toast.error(e.message),
      },
    )

  return (
    <div className="grid gap-3">
      <Label>{t('autoDeploy.triggerRules')}</Label>
      {triggers && triggers.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {triggers.map((tr) => (
            <li
              key={tr.id}
              className="flex items-center gap-3 rounded-md border bg-muted/40 px-3 py-2"
            >
              <Badge variant="secondary" className="uppercase">
                {tr.event}
              </Badge>
              <span className="flex-1 truncate font-mono text-xs">
                {tr.filter || (
                  <span className="text-muted-foreground">{t('autoDeploy.anyRef')}</span>
                )}
              </span>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t('autoDeploy.deleteTrigger')}
                disabled={remove.isPending}
                onClick={() => remove.mutate(tr.id, { onError: (e) => toast.error(e.message) })}
              >
                <Trash2 className="size-3.5" />
              </Button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">{t('autoDeploy.noRules')}</p>
      )}

      <div className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label className="text-2xs text-muted-foreground">{t('autoDeploy.event')}</Label>
          <Select value={event} onValueChange={(v) => setEvent(v as TriggerEvent)}>
            <SelectTrigger size="sm" className="w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="push">{t('autoDeploy.eventPush')}</SelectItem>
              <SelectItem value="tag">{t('autoDeploy.eventTag')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="grid flex-1 gap-1.5">
          <Label className="text-2xs text-muted-foreground">
            {event === 'tag' ? t('autoDeploy.filterLabelTag') : t('autoDeploy.filterLabelBranch')}
          </Label>
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={event === 'tag' ? 'v*' : defaultBranch || 'main'}
            className="font-mono text-xs"
          />
        </div>
        <Button variant="brand" size="sm" disabled={create.isPending} onClick={addRule}>
          {t('autoDeploy.addRule')}
        </Button>
      </div>
    </div>
  )
}

function WebhookConfigCard({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: config } = useWebhookConfig(appId)
  const regenerate = useRegenerateWebhook(appId)
  const disable = useDisableWebhook(appId)
  // The plaintext secret is returned once on (re)generate; hold it locally so the
  // operator can copy it before it's gone for good.
  const [revealed, setRevealed] = useState<string | null>(null)

  if (!config) return null

  const doRegenerate = () =>
    regenerate.mutate(undefined, {
      onSuccess: (res) => {
        setRevealed(res.secret)
        toast.success(t('autoDeploy.secretGenerated'))
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="grid gap-3">
      <Label>{t('autoDeploy.webhook')}</Label>
      <div className="grid gap-1.5">
        <Label className="text-2xs text-muted-foreground">{t('autoDeploy.payloadUrl')}</Label>
        <div className="flex items-center gap-2">
          <Input readOnly value={config.url} className="font-mono text-xs" />
          <CopyButton value={config.url} />
        </div>
      </div>

      {revealed ? (
        <div className="grid gap-1.5 rounded-md border border-brand/40 bg-brand/5 p-3">
          <Label className="text-2xs font-medium">{t('autoDeploy.secretReveal')}</Label>
          <div className="flex items-center gap-2">
            <Input readOnly value={revealed} className="font-mono text-xs" />
            <CopyButton value={revealed} />
          </div>
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground">
          {config.configured ? t('autoDeploy.secretSet') : t('autoDeploy.secretNone')}
        </span>
        <div className="flex-1" />
        <Button
          variant={config.configured ? 'outline' : 'brand'}
          size="sm"
          disabled={regenerate.isPending}
          onClick={doRegenerate}
        >
          {config.configured ? t('autoDeploy.regenerateSecret') : t('autoDeploy.generateSecret')}
        </Button>
        {config.configured ? (
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="ghost" size="sm" disabled={disable.isPending}>
                {t('autoDeploy.disable')}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('autoDeploy.disableDialogTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('autoDeploy.disableDialogDescription')}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() =>
                    disable.mutate(undefined, {
                      onSuccess: () => {
                        setRevealed(null)
                        toast.success(t('autoDeploy.webhookDisabled'))
                      },
                      onError: (e) => toast.error(e.message),
                    })
                  }
                >
                  {t('autoDeploy.disable')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        ) : null}
      </div>
    </div>
  )
}
