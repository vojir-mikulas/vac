import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { DeploysTab } from '@/features/app-detail/deploys-tab'

const APP_ID = 'app-1'

const pending = {
  id: 'dep-pending',
  app_id: APP_ID,
  status: 'pending-approval',
  triggered_at: '2026-06-18T10:00:00Z',
  triggered_by: 'push',
  rolled_back_from: null,
  started_at: null,
  finished_at: null,
  compose_hash: null,
  commit_sha: 'abcdef1234',
  commit_message: 'feat: gated release',
  error: null,
}

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

function renderTab() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <DeploysTab appId={APP_ID} />
    </QueryClientProvider>,
  )
}

describe('DeploysTab approvals', () => {
  it('shows a pending deploy and approves it', async () => {
    const approved = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = init?.method ?? 'GET'
        if (url.endsWith(`apps/${APP_ID}/deployments/pending`)) {
          return jsonResponse([pending])
        }
        if (url.endsWith(`apps/${APP_ID}/deployments/${pending.id}/approve`) && method === 'POST') {
          approved()
          return jsonResponse({ ...pending, status: 'queued' })
        }
        if (url.endsWith(`apps/${APP_ID}/deployments`)) {
          return jsonResponse([])
        }
        return jsonResponse([])
      }),
    )

    renderTab()

    expect(await screen.findByText('feat: gated release')).toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(approved).toHaveBeenCalled())
  })
})
