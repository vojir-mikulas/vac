import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { MaintenanceSection } from '@/features/app-detail/maintenance-section'

const APP_ID = 'app-1'

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
      <MaintenanceSection appId={APP_ID} />
    </QueryClientProvider>,
  )
}

describe('MaintenanceSection', () => {
  it('toggles maintenance, edits the page and resets to default', async () => {
    const set = vi.fn()
    const saved = vi.fn()
    const reset = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = init?.method ?? 'GET'
        if (url.endsWith(`apps/${APP_ID}/maintenance`) && method === 'GET') {
          return jsonResponse({
            enabled: false,
            auto: false,
            active: false,
            has_custom_page: false,
          })
        }
        if (url.endsWith(`apps/${APP_ID}/maintenance`) && method === 'PUT') {
          set(JSON.parse(String(init?.body)))
          return jsonResponse({ enabled: true, auto: false, active: true, has_custom_page: false })
        }
        if (url.endsWith(`apps/${APP_ID}/maintenance/page`) && method === 'GET') {
          return jsonResponse({ html: '<h1>default</h1>', is_default: true })
        }
        if (url.endsWith(`apps/${APP_ID}/maintenance/page`) && method === 'PUT') {
          saved(JSON.parse(String(init?.body)))
          return jsonResponse({ html: '<h1>custom</h1>', is_default: false })
        }
        if (url.endsWith(`apps/${APP_ID}/maintenance/page`) && method === 'DELETE') {
          reset(true)
          return jsonResponse({ html: '<h1>default</h1>', is_default: true })
        }
        return jsonResponse({})
      }),
    )

    renderSection()
    const user = userEvent.setup()

    // The editor seeds from the loaded page.
    const editor = (await screen.findByLabelText('Maintenance page')) as HTMLTextAreaElement
    await waitFor(() => expect(editor.value).toContain('default'))

    // Toggle maintenance on → PUT with enabled:true.
    await user.click(screen.getByLabelText('Maintenance mode'))
    await waitFor(() => expect(set).toHaveBeenCalledWith({ enabled: true, auto: false }))

    // Edit the page and save → PUT /maintenance/page.
    await user.clear(editor)
    await user.type(editor, '<h1>custom</h1>')
    await user.click(screen.getByRole('button', { name: 'Save page' }))
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ html: '<h1>custom</h1>' }))
  })
})
