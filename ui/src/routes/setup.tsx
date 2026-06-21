import { useId, useState } from 'react'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { AuthShell } from '@/components/auth/auth-shell'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api/client'
import { useDocumentTitle } from '@/lib/use-document-title'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'

const MIN_PASSWORD = 12

type SetupSearch = { token?: string }

export const Route = createFileRoute('/setup')({
  validateSearch: (search: Record<string, unknown>): SetupSearch => ({
    token: typeof search.token === 'string' ? search.token : undefined,
  }),
  beforeLoad: async ({ context }) => {
    // Setup is only reachable before an admin exists.
    const setup = await context.queryClient.ensureQueryData({
      queryKey: queryKeys.setup.status,
      queryFn: () => setupApi.status(),
    })
    if (!setup.needs_setup) throw redirect({ to: '/apps' })
  },
  component: SetupPage,
})

function SetupPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { token: tokenFromUrl } = Route.useSearch()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [token, setToken] = useState(tokenFromUrl ?? '')
  const pwErrId = useId()
  const confirmErrId = useId()
  useDocumentTitle('Set up')

  const mismatch = confirm.length > 0 && password !== confirm
  const tooShort = password.length > 0 && password.length < MIN_PASSWORD

  const create = useMutation({
    mutationFn: () => setupApi.createAdmin(username, password, token),
    onSuccess: (user) => {
      // The /setup POST issues the session and consumes the token, so the admin
      // now exists and is logged in. Seed both gate queries directly: the /_app
      // beforeLoad reads them via ensureQueryData, which returns cached data
      // without refetching — invalidating wouldn't refetch here (no active
      // observer), so a stale needs_setup:true would bounce us back to /setup.
      qc.setQueryData(queryKeys.auth.me, user)
      qc.setQueryData(queryKeys.setup.status, { needs_setup: false, token_required: false })
      void navigate({ to: '/apps' })
    },
  })

  const canSubmit =
    username.length > 0 &&
    password.length >= MIN_PASSWORD &&
    password === confirm &&
    token.trim().length > 0

  return (
    <AuthShell
      title="Welcome to VAC"
      description="Create the administrator account to get started."
    >
      <Card>
        <CardContent className="pt-6">
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (canSubmit) create.mutate()
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="username">Username</Label>
              <Input
                id="username"
                autoComplete="username"
                autoFocus
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="new-password"
                required
                aria-invalid={tooShort || undefined}
                aria-describedby={tooShort ? pwErrId : undefined}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              {tooShort ? (
                <p id={pwErrId} className="text-2xs text-muted-foreground">
                  At least {MIN_PASSWORD} characters.
                </p>
              ) : null}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="confirm">Confirm password</Label>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                required
                aria-invalid={mismatch || undefined}
                aria-describedby={mismatch ? confirmErrId : undefined}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
              {mismatch ? (
                <p id={confirmErrId} role="alert" className="text-2xs text-err-foreground">
                  Passwords don't match.
                </p>
              ) : null}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="setup-token">Setup token</Label>
              <Input
                id="setup-token"
                autoComplete="off"
                required
                spellCheck={false}
                value={token}
                onChange={(e) => setToken(e.target.value)}
                className="font-mono text-xs"
              />
              <p className="text-2xs text-muted-foreground">
                Printed in the VAC server logs on first boot, and stored at{' '}
                <code className="font-mono">$VAC_WORK_DIR/setup.token</code>.
              </p>
            </div>
            {create.error ? (
              <p role="alert" className="text-sm text-err-foreground">
                {create.error instanceof ApiError ? create.error.message : 'Something went wrong'}
              </p>
            ) : null}
            <Button type="submit" variant="brand" disabled={!canSubmit || create.isPending}>
              Create account
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthShell>
  )
}
