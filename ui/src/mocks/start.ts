// Entry point for the mock backend. Imported dynamically (and only) when
// VITE_MOCK is set, so none of this — nor any fixture/handler code — ships in
// a normal production build.

import { installFetchMock } from './fetch-mock'
import { installWebSocketMock } from './ws-mock'

export function startMocks(): void {
  // A CSRF cookie so the api client's double-submit header path is exercised.
  if (!document.cookie.includes('vac_csrf=')) {
    document.cookie = 'vac_csrf=mock-csrf-token; path=/; SameSite=Lax'
  }
  installFetchMock()
  installWebSocketMock()
  console.info('[vac] mock backend active — no real API is being contacted')
}
