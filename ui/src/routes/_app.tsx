import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

import { AppShell } from '@/components/layout/app-shell'
import { StepUpProvider } from '@/components/auth/step-up-provider'
import { ApiError } from '@/lib/api/client'
import { authApi } from '@/lib/api/auth'
import { setupApi } from '@/lib/api/setup'
import { queryKeys } from '@/lib/query/keys'

export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ context }) => {
    const { queryClient } = context

    // First boot: no admin user yet → send to the onboarding wizard.
    // staleTime: Infinity so this gate never triggers a *blocking* refetch on
    // navigation — once resolved it returns the cached value synchronously.
    // Without it, the global 30s staleTime makes every navigation after that
    // window await a fresh round-trip here, freezing on the prior page if the
    // request is slow.
    const setup = await queryClient.ensureQueryData({
      queryKey: queryKeys.setup.status,
      queryFn: () => setupApi.status(),
      staleTime: Infinity,
    })
    if (setup.needs_setup) throw redirect({ to: '/setup' })

    // Require a session; 401 → login. Same staleTime rationale as above — the
    // server enforces auth on every API call, so this is only a routing gate.
    try {
      await queryClient.ensureQueryData({
        queryKey: queryKeys.auth.me,
        queryFn: () => authApi.me(),
        staleTime: Infinity,
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
    <StepUpProvider>
      <AppShell>
        <Outlet />
      </AppShell>
    </StepUpProvider>
  )
}
