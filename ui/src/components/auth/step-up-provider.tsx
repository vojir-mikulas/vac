import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { OtpCodeField } from '@/components/common/otp-code-field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ApiError, registerStepUpHandler } from '@/lib/api/client'
import { authApi, useMe } from '@/lib/api/auth'

type Pending = { resolve: () => void; reject: (e: unknown) => void }

// StepUpProvider wires the global step-up prompt. When any API call fails with
// 403 step_up_required, the client invokes the handler registered here, which
// opens a modal asking for a fresh re-authentication — a 2FA code when TOTP is
// enabled, or a password re-entry when it isn't. On success the original request
// is replayed; on cancel it rejects with the original error.
//
// Concurrent challenges are coalesced: several destructive requests landing at
// once share one modal and all replay together once the challenge is accepted.
export function StepUpProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation('common')
  const errId = useId()
  const { data: me } = useMe()
  // When TOTP isn't set up, the step-up factor is the account password.
  const totpEnabled = !!me?.totp_enabled
  const pending = useRef<Pending[]>([])
  const [open, setOpen] = useState(false)
  const [code, setCode] = useState('')
  const [useRecovery, setUseRecovery] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    registerStepUpHandler(
      () =>
        new Promise<void>((resolve, reject) => {
          pending.current.push({ resolve, reject })
          setOpen(true)
        }),
    )
    return () => registerStepUpHandler(null)
  }, [])

  const reset = () => {
    setCode('')
    setUseRecovery(false)
    setError(null)
    setSubmitting(false)
  }

  // settle resolves or rejects every queued challenge, then closes the modal.
  const settle = (verified: boolean) => {
    const queued = pending.current
    pending.current = []
    setOpen(false)
    reset()
    for (const p of queued) {
      if (verified) p.resolve()
      else p.reject(new Error('step-up cancelled'))
    }
  }

  const submit = async () => {
    if (submitting) return
    setSubmitting(true)
    setError(null)
    try {
      await authApi.stepUp(
        !totpEnabled ? { password: code } : useRecovery ? { recovery_code: code } : { code },
      )
      settle(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('stepUp.error'))
      setSubmitting(false)
      // Reset the OTP slots for a clean retry; a password or long recovery code
      // is kept so the user can correct it.
      if (totpEnabled && !useRecovery) setCode('')
    }
  }

  return (
    <>
      {children}
      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : settle(false))}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{totpEnabled ? t('stepUp.title') : t('stepUp.passwordTitle')}</DialogTitle>
            <DialogDescription>
              {totpEnabled ? t('stepUp.description') : t('stepUp.passwordDescription')}
            </DialogDescription>
          </DialogHeader>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              submit()
            }}
          >
            {!totpEnabled ? (
              <div className="grid gap-2">
                <Label htmlFor="stepup-code">{t('stepUp.passwordLabel')}</Label>
                <Input
                  id="stepup-code"
                  autoFocus
                  required
                  type="password"
                  autoComplete="current-password"
                  aria-invalid={!!error || undefined}
                  aria-describedby={error ? errId : undefined}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </div>
            ) : useRecovery ? (
              <div className="grid gap-2">
                <Label htmlFor="stepup-code">{t('stepUp.recoveryLabel')}</Label>
                <Input
                  id="stepup-code"
                  autoFocus
                  required
                  inputMode="text"
                  autoComplete="one-time-code"
                  aria-invalid={!!error || undefined}
                  aria-describedby={error ? errId : undefined}
                  placeholder={t('stepUp.recoveryPlaceholder')}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="text-center font-mono tracking-widest"
                />
              </div>
            ) : (
              <OtpCodeField
                id="stepup-code"
                label={t('stepUp.codeLabel')}
                value={code}
                onChange={setCode}
                onComplete={submit}
                disabled={submitting}
                autoFocus
                invalid={!!error}
                describedBy={error ? errId : undefined}
              />
            )}
            {error ? (
              <p id={errId} role="alert" className="text-sm text-err-foreground">
                {error}
              </p>
            ) : null}
            {totpEnabled ? (
              <button
                type="button"
                className="self-start text-xs text-muted-foreground underline-offset-2 hover:underline"
                onClick={() => {
                  setUseRecovery((v) => !v)
                  setCode('')
                  setError(null)
                }}
              >
                {useRecovery ? t('stepUp.useAuthenticator') : t('stepUp.useRecovery')}
              </button>
            ) : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => settle(false)}>
                {t('stepUp.cancel')}
              </Button>
              <Button
                type="submit"
                variant="brand"
                // The 6-digit minimum is a TOTP rule; recovery codes and passwords
                // have their own format, so gate length only for an authenticator code.
                disabled={submitting || (totpEnabled && !useRecovery && code.length < 6) || !code}
              >
                {t('stepUp.submit')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
