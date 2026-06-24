import { useId, useState } from 'react'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { AuthShell } from '@/components/auth/auth-shell'
import { OtpCodeField } from '@/components/common/otp-code-field'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api/client'
import { useDocumentTitle } from '@/lib/use-document-title'
import { authApi } from '@/lib/api/auth'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'
import { isTotpRequired } from '@/types/api'

export const Route = createFileRoute('/login')({
  // `next` lets the VAC login gate (internal/guard) bounce an unauthenticated
  // visitor through login and back to the guard portal. Only same-origin
  // absolute paths are accepted — never protocol-relative or external URLs — so
  // it can't be turned into an open redirect.
  validateSearch: (search: Record<string, unknown>): { next?: string } => {
    const next = typeof search.next === 'string' ? search.next : undefined
    if (next && next.startsWith('/') && !next.startsWith('//')) return { next }
    return {}
  },
  beforeLoad: async ({ context }) => {
    // If onboarding hasn't happened, the login form is meaningless.
    const setup = await context.queryClient.ensureQueryData({
      queryKey: queryKeys.setup.status,
      queryFn: () => setupApi.status(),
    })
    if (setup.needs_setup) throw redirect({ to: '/setup' })
  },
  component: LoginPage,
})

function LoginPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const errId = useId()
  const { next } = Route.useSearch()
  const [stage, setStage] = useState<'credentials' | 'totp'>('credentials')
  useDocumentTitle(stage === 'totp' ? 'Two-factor code' : 'Sign in')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [code, setCode] = useState('')

  const finish = async () => {
    await qc.invalidateQueries({ queryKey: queryKeys.auth.me })
    if (next) {
      // A full-page navigation, not an SPA route change: `next` is typically the
      // guard portal (a server endpoint, not a client route), and the reload
      // ensures the freshly-set session cookie is applied to it.
      window.location.assign(next)
      return
    }
    await navigate({ to: '/apps' })
  }

  const login = useMutation({
    mutationFn: () => authApi.login({ username, password, remember }),
    onSuccess: (res) => {
      if (isTotpRequired(res)) setStage('totp')
      else void finish()
    },
  })

  const totp = useMutation({
    mutationFn: () => authApi.totpLogin({ code }),
    onSuccess: () => void finish(),
    // Clear the slots on a bad code so the next attempt starts fresh.
    onError: () => setCode(''),
  })

  if (stage === 'totp') {
    return (
      <AuthShell
        title="Two-factor code"
        description="Enter the 6-digit code from your authenticator."
      >
        <Card>
          <CardContent className="pt-6">
            <form
              className="flex flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault()
                totp.mutate()
              }}
            >
              <OtpCodeField
                id="code"
                label="Authentication code"
                value={code}
                onChange={setCode}
                onComplete={() => totp.mutate()}
                disabled={totp.isPending}
                autoFocus
                invalid={!!totp.error}
                describedBy={totp.error ? errId : undefined}
              />
              {totp.error ? <ErrorText id={errId} error={totp.error} /> : null}
              <Button type="submit" variant="brand" disabled={totp.isPending || code.length < 6}>
                Verify
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setStage('credentials')}
              >
                Back
              </Button>
            </form>
          </CardContent>
        </Card>
      </AuthShell>
    )
  }

  return (
    <AuthShell title="Sign in to VAC" description="Vojir's Awesome Containers">
      <Card>
        <CardContent className="pt-6">
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              login.mutate()
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="username">Username</Label>
              <Input
                id="username"
                autoComplete="username"
                autoFocus
                required
                aria-invalid={!!login.error || undefined}
                aria-describedby={login.error ? errId : undefined}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                aria-invalid={!!login.error || undefined}
                aria-describedby={login.error ? errId : undefined}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input
                type="checkbox"
                checked={remember}
                onChange={(e) => setRemember(e.target.checked)}
                className="size-4 accent-brand"
              />
              Remember this device
            </label>
            {login.error ? <ErrorText id={errId} error={login.error} /> : null}
            <Button type="submit" variant="brand" disabled={login.isPending}>
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthShell>
  )
}

function ErrorText({ id, error }: { id?: string; error: unknown }) {
  const message = error instanceof ApiError ? error.message : 'Something went wrong'
  return (
    <p id={id} role="alert" className="text-sm text-err-foreground">
      {message}
    </p>
  )
}
