import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createMemoryHistory, createRouter } from '@tanstack/react-router'
import { axe } from 'vitest-axe'

import { routeTree } from '@/routeTree.gen'
import { ThemeProvider } from '@/components/theme/theme-provider'
import { TooltipProvider } from '@/components/ui/tooltip'

// A tripwire, not full coverage. Renders the app shell + a representative page
// through real axe-core and asserts no gross violations (missing labels,
// duplicate ids, orphaned controls). Catches the regressions a human review of
// a new feature would otherwise have to spot by hand.

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const HOST_STATS = {
  host_ip: '10.0.0.1',
  cpu_percent: 12,
  mem_used_bytes: 1_000_000,
  mem_total_bytes: 8_000_000,
  disk_used_bytes: 5_000_000,
  disk_total_bytes: 50_000_000,
  request_rate: 1.5,
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function renderAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const result = render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <TooltipProvider>
          <RouterProvider router={router} />
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  )
  return result
}

describe('accessibility smoke', () => {
  it('app shell + apps dashboard has no axe violations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/api/host/stats') || url.includes('/api/metrics/host')) {
          return jsonResponse(HOST_STATS)
        }
        if (url.includes('/api/apps')) return jsonResponse([])
        return jsonResponse({})
      }),
    )

    const { container } = renderAt('/apps')
    // Wait for the shell to settle on a stable landmark before auditing.
    await screen.findByRole('heading', { name: 'Apps' })

    // jsdom can't lay out text, so color-contrast is non-deterministic there —
    // leave it to the manual/CI checks and keep the structural rules here.
    const results = await axe(container, {
      rules: { 'color-contrast': { enabled: false } },
    })
    expect(results).toHaveNoViolations()
  })

  it('login screen has no axe violations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/api/setup/status')) return jsonResponse({ needs_setup: false })
        return jsonResponse({})
      }),
    )

    const { container } = renderAt('/login')
    await screen.findByText('Sign in to VAC')

    const results = await axe(container, {
      rules: { 'color-contrast': { enabled: false } },
    })
    expect(results).toHaveNoViolations()
  })
})
