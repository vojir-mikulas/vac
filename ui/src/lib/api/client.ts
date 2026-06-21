// Thin typed fetch wrapper around the VAC API.
//
// - Always sends the session cookie (`credentials: 'include'`).
// - Adds the CSRF double-submit header on mutating verbs, reading the
//   JS-readable `vac_csrf` cookie.
// - Throws a typed `ApiError` on non-2xx so callers / TanStack Query can
//   branch on the stable error `code`.

const BASE = '/api'
const CSRF_COOKIE = 'vac_csrf'
const CSRF_HEADER = 'X-CSRF-Token'
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

export interface ApiErrorBody {
  error: string
  code: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

// step_up_required is the stable error code the backend returns (403) when a
// destructive action needs fresh 2FA. A registered handler (the StepUpProvider)
// prompts for a code; on success the request is transparently retried once.
const STEP_UP_CODE = 'step_up_required'

export type StepUpHandler = () => Promise<void>
let stepUpHandler: StepUpHandler | null = null

// registerStepUpHandler wires the global step-up prompt. Passing null clears it
// (e.g. on provider unmount). Only one handler is active at a time.
export function registerStepUpHandler(fn: StepUpHandler | null): void {
  stepUpHandler = fn
}

// An unexpected 401 means the session expired or was revoked mid-session. The
// route gate only redirects on navigation, so without this a 401 on an in-page
// query/mutation leaves stale data on screen with no way back to login.
export type UnauthorizedHandler = () => void
let unauthorizedHandler: UnauthorizedHandler | null = null

// registerUnauthorizedHandler wires the global session-expiry handler (clears
// caches + redirects to /login). Passing null clears it. The pre-auth login and
// TOTP endpoints are exempt — a failed login is not a session expiry.
export function registerUnauthorizedHandler(fn: UnauthorizedHandler | null): void {
  unauthorizedHandler = fn
}

const PREAUTH_PATHS = new Set(['auth/login', 'auth/totp'])

function readCookie(name: string): string | null {
  const prefix = name + '='
  const parts = document.cookie ? document.cookie.split('; ') : []
  for (const part of parts) {
    if (part.startsWith(prefix)) return decodeURIComponent(part.slice(prefix.length))
  }
  return null
}

interface RequestOptions {
  method?: string
  body?: unknown
  // rawBody sends a string verbatim (with contentType) instead of JSON-encoding
  // `body` — used for the portability specs, which travel as YAML text.
  rawBody?: string
  contentType?: string
  // responseType 'text' returns the raw response body unparsed (e.g. an exported
  // YAML spec); 'blob' returns the body as a Blob (binary downloads, e.g. the
  // migration bundle tar); the default parses JSON.
  responseType?: 'json' | 'text' | 'blob'
  signal?: AbortSignal
  // `path` is relative to /api unless it starts with '/', in which case it's
  // treated as an absolute server path (used for non-/api routes).
}

async function request<T>(path: string, opts: RequestOptions = {}, retried = false): Promise<T> {
  const method = opts.method ?? 'GET'
  const url = path.startsWith('/') ? path : `${BASE}/${path}`

  const headers: Record<string, string> = {}
  let body: BodyInit | undefined

  if (opts.rawBody !== undefined) {
    headers['Content-Type'] = opts.contentType ?? 'text/plain'
    body = opts.rawBody
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }

  if (!SAFE_METHODS.has(method)) {
    const token = readCookie(CSRF_COOKIE)
    if (token) headers[CSRF_HEADER] = token
  }

  const res = await fetch(url, {
    method,
    headers,
    body,
    credentials: 'include',
    signal: opts.signal,
  })

  if (!res.ok) {
    let code = 'internal_error'
    let message = res.statusText
    try {
      const parsed = (await res.json()) as ApiErrorBody
      if (parsed?.code) code = parsed.code
      if (parsed?.error) message = parsed.error
    } catch {
      // non-JSON error body — keep status text
    }
    // Session expired/revoked mid-session: hand off to the global handler so the
    // UI redirects to login instead of stranding the user on stale data. Skip
    // the pre-auth endpoints (a failed login/TOTP is not an expiry).
    if (res.status === 401 && unauthorizedHandler && !PREAUTH_PATHS.has(path)) {
      unauthorizedHandler()
    }
    // Step-up: the action needs fresh 2FA. Prompt once, then replay the request.
    // If the user cancels (handler rejects), surface the original error so the
    // caller's onError sees step_up_required rather than a synthetic message.
    if (res.status === 403 && code === STEP_UP_CODE && stepUpHandler && !retried) {
      try {
        await stepUpHandler()
      } catch {
        throw new ApiError(res.status, code, message)
      }
      return request<T>(path, opts, true)
    }
    throw new ApiError(res.status, code, message)
  }

  if (res.status === 204) return undefined as T
  // Binary download: return the raw bytes. Kept above the text read so the body
  // isn't consumed as a string first. Step-up/CSRF retry above still applies.
  if (opts.responseType === 'blob') return (await res.blob()) as T
  const text = await res.text()
  if (opts.responseType === 'text') return text as T
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PATCH', body }),
  del: <T>(path: string, body?: unknown) => request<T>(path, { method: 'DELETE', body }),
  getText: (path: string, signal?: AbortSignal) =>
    request<string>(path, { responseType: 'text', signal }),
  postRaw: <T>(path: string, rawBody: string, contentType: string) =>
    request<T>(path, { method: 'POST', rawBody, contentType }),
  postBlob: (path: string, body?: unknown) =>
    request<Blob>(path, { method: 'POST', body, responseType: 'blob' }),
}
