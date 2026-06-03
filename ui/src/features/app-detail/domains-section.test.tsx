import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { AppDomainsSection } from '@/features/app-detail/domains-section'
import { TooltipProvider } from '@/components/ui/tooltip'

const APP_ID = 'app-1'

const customDomain = {
  id: 'dom-1',
  app_id: APP_ID,
  service_name: 'web',
  hostname: 'shop.example.com',
  type: 'custom',
  managed: false,
  status: 'active',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T00:00:00Z',
}

const autoDomain = {
  id: '',
  app_id: APP_ID,
  service_name: 'web',
  hostname: 'app-1--web.apps.example.com',
  type: 'auto',
  managed: true,
  status: 'active',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T00:00:00Z',
}

const services = [{ id: 'svc-1', name: 'web' }]

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function renderSection() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <AppDomainsSection appId={APP_ID} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

describe('AppDomainsSection', () => {
  it('lists custom + auto domains and supports add', async () => {
    const created = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = init?.method ?? 'GET'
        if (url.includes(`apps/${APP_ID}/domains`) && method === 'GET') {
          return jsonResponse([customDomain, autoDomain])
        }
        if (url.includes(`apps/${APP_ID}/services`) && method === 'GET') {
          return jsonResponse(services)
        }
        if (url.includes(`apps/${APP_ID}/services/web/domains`) && method === 'POST') {
          created(JSON.parse(String(init?.body)))
          return jsonResponse({ ...customDomain, id: 'dom-2', hostname: 'new.example.com' })
        }
        return jsonResponse({})
      }),
    )

    renderSection()

    // Custom domain renders with a Delete control.
    const customRow = (await screen.findByText('shop.example.com')).closest('div.flex.flex-col')!
    expect(within(customRow as HTMLElement).getByRole('button', { name: 'Delete' })).toBeTruthy()

    // Auto domain is read-only: an "Auto" badge and no Delete.
    const autoRow = screen.getByText('app-1--web.apps.example.com').closest('div.flex.flex-col')!
    expect(within(autoRow as HTMLElement).getByText('Auto')).toBeTruthy()
    expect(within(autoRow as HTMLElement).queryByRole('button', { name: 'Delete' })).toBeNull()

    // Add a domain against the picked service.
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('app.example.com'), 'new.example.com')
    await user.selectOptions(screen.getByRole('combobox'), 'web')
    await user.click(screen.getByRole('button', { name: 'Add domain' }))

    await waitFor(() => expect(created).toHaveBeenCalledWith({ hostname: 'new.example.com' }))
  })
})
