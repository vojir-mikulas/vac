import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { CopyButton } from '@/components/common/copy-button'
import { authApi, useMe } from '@/lib/api/auth'
import { queryKeys } from '@/lib/query/keys'
import type { TotpSetup } from '@/types/api'

export function TotpSection() {
  const { data: me } = useMe()

  return (
    <section>
      <SectionHeader>Two-factor authentication</SectionHeader>
      <Card className="gap-4 p-5">
        <div className="flex items-center gap-3">
          <ShieldCheck
            className={me?.totp_enabled ? 'size-5 text-ok' : 'size-5 text-muted-foreground'}
          />
          <div className="flex-1">
            <div className="text-sm font-medium">
              {me?.totp_enabled ? '2FA is enabled' : '2FA is disabled'}
            </div>
            <p className="text-xs text-muted-foreground">
              Require a time-based code in addition to your password.
            </p>
          </div>
          {me?.totp_enabled ? <DisableDialog /> : <EnableFlow />}
        </div>
      </Card>
    </section>
  )
}

function EnableFlow() {
  const qc = useQueryClient()
  const [setup, setSetup] = useState<TotpSetup | null>(null)
  const [code, setCode] = useState('')
  const [recovery, setRecovery] = useState<string[] | null>(null)

  const begin = useMutation({
    mutationFn: () => authApi.totpSetup(),
    onSuccess: (s) => setSetup(s),
    onError: (e) => toast.error(e.message),
  })

  const enable = useMutation({
    mutationFn: () => authApi.totpEnable(code),
    onSuccess: (r) => {
      setRecovery(r.recovery_codes)
      qc.invalidateQueries({ queryKey: queryKeys.auth.me })
    },
    onError: (e) => toast.error(e.message),
  })

  const open = begin.data != null || begin.isPending
  const onOpenChange = (next: boolean) => {
    if (!next) {
      setSetup(null)
      setCode('')
      setRecovery(null)
      begin.reset()
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <Button variant="brand" size="sm" disabled={begin.isPending} onClick={() => begin.mutate()}>
        Enable
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {recovery ? 'Save your recovery codes' : 'Set up two-factor authentication'}
          </DialogTitle>
        </DialogHeader>

        {recovery ? (
          <div className="flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">
              Store these somewhere safe. Each code can be used once if you lose your device.
            </p>
            <div className="grid grid-cols-2 gap-2 rounded-md border bg-surface-1 p-3 font-mono text-xs">
              {recovery.map((c) => (
                <span key={c}>{c}</span>
              ))}
            </div>
            <CopyButton value={recovery.join('\n')} label="Copy codes" />
          </div>
        ) : setup ? (
          <div className="flex flex-col gap-4">
            <div className="flex justify-center rounded-md border bg-white p-4">
              <QRCodeSVG value={setup.otpauth_uri} size={160} />
            </div>
            <div className="flex items-center justify-between gap-2 rounded-md border bg-surface-1 px-3 py-2">
              <code className="truncate font-mono text-xs">{setup.secret}</code>
              <CopyButton value={setup.secret} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="totp-code">Enter the 6-digit code</Label>
              <Input
                id="totp-code"
                inputMode="numeric"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="text-center font-mono tracking-widest"
              />
            </div>
          </div>
        ) : null}

        {!recovery && setup ? (
          <DialogFooter>
            <Button
              variant="brand"
              disabled={enable.isPending || code.length < 6}
              onClick={() => enable.mutate()}
            >
              Verify & enable
            </Button>
          </DialogFooter>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

function DisableDialog() {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')

  const disable = useMutation({
    mutationFn: () => authApi.totpDisable(password),
    onSuccess: () => {
      toast.success('2FA disabled')
      qc.invalidateQueries({ queryKey: queryKeys.auth.me })
      setOpen(false)
      setPassword('')
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="danger" size="sm">
          Disable
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Disable two-factor authentication</DialogTitle>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="pw">Confirm your password</Label>
          <Input
            id="pw"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <DialogFooter>
          <Button
            variant="danger"
            disabled={disable.isPending || !password}
            onClick={() => disable.mutate()}
          >
            Disable 2FA
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
