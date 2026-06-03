import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useMutation } from '@tanstack/react-query'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
import { instanceApi } from '@/lib/api/instance'

const RESET_PHRASE = 'RESET'

export function DangerZoneSection() {
  const { t } = useTranslation('settings')
  return (
    <section>
      <SectionHeader>{t('dangerZone.heading')}</SectionHeader>
      <div className="flex flex-col gap-0 overflow-hidden rounded-xl border">
        <RestartControlPlaneRow />
        <StopAllAppsRow />
        <ResetInstanceRow />
      </div>
    </section>
  )
}

function DangerRow({
  title,
  description,
  children,
  border,
  danger,
}: {
  title: string
  description: string
  children: React.ReactNode
  border?: boolean
  danger?: boolean
}) {
  return (
    <div
      className={`flex items-center justify-between gap-4 px-5 py-4 ${
        border ? 'border-t' : ''
      } ${danger ? 'bg-err-bg/40' : ''}`}
    >
      <div className="min-w-0">
        <div className={`text-sm font-medium ${danger ? 'text-err-foreground' : ''}`}>{title}</div>
        <p className="text-xs text-muted-foreground">{description}</p>
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function RestartControlPlaneRow() {
  const { t } = useTranslation('settings')
  const [reconnecting, setReconnecting] = useState(false)
  const restart = useMutation({
    mutationFn: () => instanceApi.restartControlPlane(),
    onSuccess: () => {
      setReconnecting(true)
      toast.info(t('dangerZone.restart.toast'))
      // The API briefly drops; a full reload once it's back is the cleanest reset.
      setTimeout(() => window.location.reload(), 6000)
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title={t('dangerZone.restart.title')}
      description={t('dangerZone.restart.description')}
    >
      <ConfirmButton
        label={reconnecting ? t('dangerZone.restart.reconnecting') : t('dangerZone.restart.action')}
        title={t('dangerZone.restart.confirmTitle')}
        description={t('dangerZone.restart.confirmDescription')}
        actionLabel={t('dangerZone.restart.action')}
        disabled={reconnecting || restart.isPending}
        onConfirm={() => restart.mutate()}
      />
    </DangerRow>
  )
}

function StopAllAppsRow() {
  const { t } = useTranslation('settings')
  const stop = useMutation({
    mutationFn: () => instanceApi.stopAllApps(),
    onSuccess: (r) => toast.success(t('dangerZone.stopAll.toast', { count: r.stopped })),
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title={t('dangerZone.stopAll.title')}
      description={t('dangerZone.stopAll.description')}
      border
    >
      <ConfirmButton
        label={t('dangerZone.stopAll.action')}
        title={t('dangerZone.stopAll.confirmTitle')}
        description={t('dangerZone.stopAll.confirmDescription')}
        actionLabel={t('dangerZone.stopAll.action')}
        disabled={stop.isPending}
        onConfirm={() => stop.mutate()}
      />
    </DangerRow>
  )
}

function ResetInstanceRow() {
  const { t } = useTranslation('settings')
  const [open, setOpen] = useState(false)
  const [phrase, setPhrase] = useState('')
  const reset = useMutation({
    mutationFn: () => instanceApi.reset(RESET_PHRASE),
    onSuccess: (r) => {
      toast.success(t('dangerZone.reset.toast', { count: r.removed }))
      setOpen(false)
      setPhrase('')
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title={t('dangerZone.reset.title')}
      description={t('dangerZone.reset.description')}
      border
      danger
    >
      <AlertDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) setPhrase('')
        }}
      >
        <AlertDialogTrigger asChild>
          <Button variant="destructive" size="sm">
            {t('dangerZone.reset.action')}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('dangerZone.reset.confirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              <Trans
                t={t}
                i18nKey="dangerZone.reset.confirmDescription"
                values={{ phrase: RESET_PHRASE }}
                components={[<span className="font-mono font-semibold" />]}
              />
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Input
            value={phrase}
            onChange={(e) => setPhrase(e.target.value)}
            placeholder={RESET_PHRASE}
            className="font-mono"
            autoFocus
          />
          <AlertDialogFooter>
            <AlertDialogCancel>{t('dangerZone.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={phrase !== RESET_PHRASE || reset.isPending}
              onClick={(e) => {
                // Keep the dialog open if the phrase is wrong (defensive).
                if (phrase !== RESET_PHRASE) {
                  e.preventDefault()
                  return
                }
                e.preventDefault()
                reset.mutate()
              }}
            >
              {reset.isPending
                ? t('dangerZone.reset.resetting')
                : t('dangerZone.reset.confirmAction')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DangerRow>
  )
}

function ConfirmButton({
  label,
  title,
  description,
  actionLabel,
  disabled,
  onConfirm,
}: {
  label: string
  title: string
  description: string
  actionLabel: string
  disabled?: boolean
  onConfirm: () => void
}) {
  const { t } = useTranslation('settings')
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" disabled={disabled}>
          {label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('dangerZone.cancel')}</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>{actionLabel}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
