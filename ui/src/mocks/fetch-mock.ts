// Overrides window.fetch so every `/api/**` request is served from the mock
// store. Non-API requests (static assets, /health) pass through untouched.
// This is the HTTP half of the mock seam — the UI's api client (which is the
// only caller of fetch) never knows the difference.

import { handleRequest, MockHttpError } from './handlers'
import { delay, randInt } from './util'

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export function installFetchMock(): void {
  const original = window.fetch.bind(window)

  const mockFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const rawUrl = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const url = new URL(rawUrl, window.location.origin)

    if (!url.pathname.startsWith('/api/')) return original(input, init)

    const method = (
      init?.method ?? (typeof input === 'object' && 'method' in input ? input.method : 'GET')
    ).toUpperCase()
    const apiPath = url.pathname.slice('/api/'.length)

    let body: unknown
    const rawBody = init?.body
    if (typeof rawBody === 'string' && rawBody.length > 0) {
      try {
        body = JSON.parse(rawBody)
      } catch {
        body = undefined
      }
    }

    // Simulate a little network latency so loading states are exercised.
    await delay(randInt(80, 260))

    try {
      const res = await handleRequest(method, apiPath, url.searchParams, body)
      if (res.status === 204 || res.body === undefined) return new Response(null, { status: 204 })
      return jsonResponse(res.body, res.status)
    } catch (err) {
      if (err instanceof MockHttpError) {
        return jsonResponse({ error: err.message, code: err.code }, err.status)
      }
      return jsonResponse({ error: String(err), code: 'internal_error' }, 500)
    }
  }

  // The api client calls the global `fetch`; in a browser window === globalThis,
  // but assign both so interception holds regardless of how it's referenced.
  window.fetch = mockFetch
  globalThis.fetch = mockFetch
}
