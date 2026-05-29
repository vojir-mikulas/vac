import { useState } from 'react'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { AuthShell } from '@/components/auth/auth-shell'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api/client'
import { authApi } from '@/lib/api/auth'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'
import { isTotpRequired } from '@/types/api'

export const Route = createFileRoute('/login')({
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
  const [stage, setStage] = useState<'credentials' | 'totp'>('credentials')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [code, setCode] = useState('')

  const finish = async () => {
    await qc.invalidateQueries({ queryKey: queryKeys.auth.me })
    navigate({ to: '/apps' })
  }

  const login = useMutation({
    mutationFn: () => authApi.login({ username, password, remember }),
    onSuccess: (res) => {
      if (isTotpRequired(res)) setStage('totp')
      else finish()
    },
  })

  const totp = useMutation({
    mutationFn: () => authApi.totpLogin({ code }),
    onSuccess: () => finish(),
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
              <div className="grid gap-2">
                <Label htmlFor="code">Authentication code</Label>
                <Input
                  id="code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  autoFocus
                  placeholder="123456"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="text-center font-mono tracking-widest"
                />
              </div>
              {totp.error ? <ErrorText error={totp.error} /> : null}
              <Button type="submit" variant="brand" disabled={totp.isPending}>
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
            {login.error ? <ErrorText error={login.error} /> : null}
            <Button type="submit" variant="brand" disabled={login.isPending}>
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthShell>
  )
}

function ErrorText({ error }: { error: unknown }) {
  const message = error instanceof ApiError ? error.message : 'Something went wrong'
  return <p className="text-sm text-err-foreground">{message}</p>
}
