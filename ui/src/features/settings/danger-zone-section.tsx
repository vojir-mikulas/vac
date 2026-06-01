import { useState } from 'react'
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
  return (
    <section>
      <SectionHeader>Danger zone</SectionHeader>
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
  const [reconnecting, setReconnecting] = useState(false)
  const restart = useMutation({
    mutationFn: () => instanceApi.restartControlPlane(),
    onSuccess: () => {
      setReconnecting(true)
      toast.info('Control plane restarting — reconnecting…')
      // The API briefly drops; a full reload once it's back is the cleanest reset.
      setTimeout(() => window.location.reload(), 6000)
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title="Restart control plane"
      description="Restarts vac-api and vac-proxy. Apps keep running."
    >
      <ConfirmButton
        label={reconnecting ? 'Reconnecting…' : 'Restart'}
        title="Restart control plane?"
        description="vac-api and vac-proxy will restart. The dashboard will briefly disconnect and reconnect. Running apps are unaffected."
        actionLabel="Restart"
        disabled={reconnecting || restart.isPending}
        onConfirm={() => restart.mutate()}
      />
    </DangerRow>
  )
}

function StopAllAppsRow() {
  const stop = useMutation({
    mutationFn: () => instanceApi.stopAllApps(),
    onSuccess: (r) => toast.success(`Stopped ${r.stopped} app${r.stopped === 1 ? '' : 's'}`),
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title="Stop all applications"
      description="Stops every container managed by VAC."
      border
    >
      <ConfirmButton
        label="Stop all"
        title="Stop all applications?"
        description="Every VAC-managed app stack will be stopped. They can be started again individually. The control plane keeps running."
        actionLabel="Stop all"
        disabled={stop.isPending}
        onConfirm={() => stop.mutate()}
      />
    </DangerRow>
  )
}

function ResetInstanceRow() {
  const [open, setOpen] = useState(false)
  const [phrase, setPhrase] = useState('')
  const reset = useMutation({
    mutationFn: () => instanceApi.reset(RESET_PHRASE),
    onSuccess: (r) => {
      toast.success(`Reset complete — removed ${r.removed} app${r.removed === 1 ? '' : 's'}`)
      setOpen(false)
      setPhrase('')
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <DangerRow
      title="Reset instance"
      description="Wipes apps, deployments, and databases. Requires typed confirmation."
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
            Reset…
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset this instance?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently removes every app, its deployments, and its data volumes. This cannot
              be undone. Type <span className="font-mono font-semibold">{RESET_PHRASE}</span> to
              confirm.
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
            <AlertDialogCancel>Cancel</AlertDialogCancel>
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
              {reset.isPending ? 'Resetting…' : 'Reset instance'}
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
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>{actionLabel}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
