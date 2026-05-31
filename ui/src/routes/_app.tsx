import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

import { AppShell } from '@/components/layout/app-shell'
import { ApiError } from '@/lib/api/client'
import { authApi } from '@/lib/api/auth'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'

export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ context }) => {
    // TEMP (local UI preview, do not commit): skip auth/setup guard so the
    // dashboard renders without a backend.
    return
    const { queryClient } = context

    // First boot: no admin user yet → send to the onboarding wizard.
    const setup = await queryClient.ensureQueryData({
      queryKey: queryKeys.setup.status,
      queryFn: () => setupApi.status(),
    })
    if (setup.needs_setup) throw redirect({ to: '/setup' })

    // Require a session; 401 → login.
    try {
      await queryClient.ensureQueryData({
        queryKey: queryKeys.auth.me,
        queryFn: () => authApi.me(),
      })
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        throw redirect({ to: '/login' })
      }
      throw err
    }
  },
  component: AppLayout,
})

function AppLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  )
}
