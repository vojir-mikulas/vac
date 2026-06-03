import { StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { LazyMotion, MotionConfig, domAnimation } from 'motion/react'

import './index.css'
// Initialize the i18n singleton before anything renders. English is bundled, so
// this resolves synchronously today; the <Suspense> below covers lazy locale
// loads once additional languages ship.
import './i18n'
import { routeTree } from './routeTree.gen'
import { ThemeProvider } from '@/components/theme/theme-provider'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import {
  NotFoundScreen,
  RouteErrorScreen,
  RoutePendingScreen,
} from '@/components/common/route-states'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: true,
    },
  },
})

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
  // If a route's gate (beforeLoad/loader) runs long, show a spinner instead of
  // leaving the user on the prior page with the URL already changed.
  defaultPendingComponent: RoutePendingScreen,
  defaultPendingMs: 150,
  defaultPendingMinMs: 300,
  defaultNotFoundComponent: NotFoundScreen,
  defaultErrorComponent: ({ error }) => <RouteErrorScreen error={error} />,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

async function bootstrap() {
  // Mock backend: when VITE_MOCK is set, intercept fetch/WebSocket so the whole
  // UI runs with no real API (used for the deployable static preview).
  if (import.meta.env.VITE_MOCK) {
    const { startMocks } = await import('./mocks/start')
    startMocks()
  }

  const rootEl = document.getElementById('root')
  if (!rootEl) throw new Error('#root element not found')

  createRoot(rootEl).render(
    <StrictMode>
      {/* LazyMotion + domAnimation: load only the DOM animation features (~5kb)
          and use `m.*` components instead of the full `motion.*` bundle.
          MotionConfig reducedMotion="user" disables all motion under the OS
          reduce-motion setting — complements the CSS override in index.css. */}
      <LazyMotion features={domAnimation} strict>
        <MotionConfig reducedMotion="user">
          <QueryClientProvider client={queryClient}>
            <ThemeProvider>
              <TooltipProvider delayDuration={200}>
                <Suspense fallback={null}>
                  <RouterProvider router={router} />
                </Suspense>
                <Toaster />
              </TooltipProvider>
            </ThemeProvider>
          </QueryClientProvider>
        </MotionConfig>
      </LazyMotion>
    </StrictMode>,
  )
}

void bootstrap()
