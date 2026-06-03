import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { LazyMotion, MotionConfig, domAnimation } from 'motion/react'

import './index.css'
import { routeTree } from './routeTree.gen'
import { ThemeProvider } from '@/components/theme/theme-provider'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { NotFoundScreen, RouteErrorScreen } from '@/components/common/route-states'

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
                <RouterProvider router={router} />
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
