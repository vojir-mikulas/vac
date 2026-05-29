import { useState } from 'react'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { AuthShell } from '@/components/auth/auth-shell'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api/client'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'

export const Route = createFileRoute('/setup')({
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
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')

  const mismatch = confirm.length > 0 && password !== confirm
  const tooShort = password.length > 0 && password.length < 8

  const create = useMutation({
    mutationFn: () => setupApi.createAdmin(username, password),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: queryKeys.setup.status })
      navigate({ to: '/login' })
    },
  })

  const canSubmit = username.length > 0 && password.length >= 8 && password === confirm

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
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              {tooShort ? (
                <p className="text-2xs text-muted-foreground">At least 8 characters.</p>
              ) : null}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="confirm">Confirm password</Label>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
              {mismatch ? (
                <p className="text-2xs text-err-foreground">Passwords don't match.</p>
              ) : null}
            </div>
            {create.error ? (
              <p className="text-sm text-err-foreground">
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
