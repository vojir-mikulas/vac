import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from '@tanstack/react-router'

import { ThemeProvider } from '@/components/theme/theme-provider'
import { CommandMenu } from './command-menu'

// jsdom gaps that cmdk/Radix touch when the palette mounts.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Element.prototype.scrollIntoView ??= () => {}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('instance/info')) {
        return jsonResponse({ managed_services: false, enable_shell: false, idle_suspend: false })
      }
      if (url.endsWith('apps')) return jsonResponse([])
      return jsonResponse({})
    }),
  )
}

// Mounts the palette (open) inside a minimal router so its useNavigate/useParams
// hooks resolve. The root route is all we need — no app is in scope.
function renderMenu() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const rootRoute = createRootRoute({
    component: function Root() {
      const [open, setOpen] = useState(true)
      return <CommandMenu open={open} onOpenChange={setOpen} />
    },
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>,
  )
}

describe('CommandMenu', () => {
  it('renders the global navigation, settings and action commands', async () => {
    stubFetch()
    renderMenu()

    // Parity items that were missing from the old palette.
    await waitFor(() => expect(screen.getByText('Logs')).toBeInTheDocument())
    expect(screen.getByText('Security')).toBeInTheDocument()
    expect(screen.getByText('Storage')).toBeInTheDocument()
    expect(screen.getByText('Danger zone')).toBeInTheDocument()
    expect(screen.getByText('Toggle theme')).toBeInTheDocument()
  })

  it('asks for confirmation before a destructive action runs', async () => {
    stubFetch()
    const user = userEvent.setup()
    renderMenu()

    await waitFor(() => expect(screen.getByText('Stop all apps')).toBeInTheDocument())
    await user.click(screen.getByText('Stop all apps'))

    // The confirm dialog takes over; the action only fires on OK.
    await waitFor(() => expect(screen.getByText('Stop all apps?')).toBeInTheDocument())
    const fetchMock = globalThis.fetch as ReturnType<typeof vi.fn>
    const stopCalled = () =>
      fetchMock.mock.calls.some((c) => String(c[0]).endsWith('instance/stop-all-apps'))
    expect(stopCalled()).toBe(false)
  })
})
