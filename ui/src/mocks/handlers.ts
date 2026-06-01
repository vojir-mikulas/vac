// Route table for the mock backend. Mirrors every endpoint the UI's API client
// calls (ui/src/lib/api/*). Each handler reads/mutates the in-memory store in
// db.ts. Errors throw MockHttpError, which the fetch shim turns into the same
// `{ error, code }` body shape the real API uses.

import type { CreateAppInput, UpdateAppInput } from '@/types/api'
import type { EnvVarInput } from '@/lib/api/env'
import {
  appMetrics,
  createApp,
  createDomain,
  deleteApp,
  deleteDomain,
  findApp,
  getState,
  hostStats,
  listAudit,
  listEnv,
  replaceEnv,
  restartService,
  revealEnv,
  revertAudit,
  setStackState,
  toPublicDeployment,
  triggerDeploy,
  updateApp,
  updateService,
} from './db'
import { daysAgoISO, fakeSha, nowISO, uid } from './util'

export class MockHttpError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

const notFound = (what = 'resource') => new MockHttpError(404, 'not_found', `${what} not found`)

interface Ctx {
  params: Record<string, string>
  query: URLSearchParams
  body: unknown
}

export interface HandlerResult {
  status?: number
  body?: unknown
}

type Handler = (ctx: Ctx) => HandlerResult | Promise<HandlerResult>

interface Route {
  method: string
  pattern: string
  handler: Handler
}

const ok = (body?: unknown): HandlerResult => ({ body })
const created = (body: unknown): HandlerResult => ({ status: 201, body })

// Resolve the app from :id or 404.
function appOr404(ctx: Ctx) {
  const app = findApp(ctx.params.id ?? '')
  if (!app) throw notFound('app')
  return app
}

const routes: Route[] = [
  // ── Setup / auth ──────────────────────────────────────────────────────────
  {
    method: 'GET',
    pattern: 'setup/status',
    handler: () => ok({ needs_setup: false, token_required: false }),
  },
  { method: 'POST', pattern: 'setup/admin', handler: () => ok(getState().user) },

  { method: 'GET', pattern: 'auth/me', handler: () => ok(getState().user) },
  { method: 'POST', pattern: 'auth/login', handler: () => ok(getState().user) },
  { method: 'POST', pattern: 'auth/totp', handler: () => ok(getState().user) },
  { method: 'POST', pattern: 'auth/logout', handler: () => ok({ status: 'ok' }) },
  {
    method: 'POST',
    pattern: 'auth/totp/setup',
    handler: () =>
      ok({
        otpauth_uri: 'otpauth://totp/VAC:admin?secret=JBSWY3DPEHPK3PXP&issuer=VAC',
        secret: 'JBSWY3DPEHPK3PXP',
      }),
  },
  {
    method: 'POST',
    pattern: 'auth/totp/enable',
    handler: () => {
      getState().user.totp_enabled = true
      return ok({ recovery_codes: ['1a2b-3c4d', '5e6f-7g8h', '9i0j-k1l2', 'm3n4-o5p6'] })
    },
  },
  {
    method: 'DELETE',
    pattern: 'auth/totp',
    handler: () => {
      getState().user.totp_enabled = false
      return ok({ status: 'ok' })
    },
  },

  { method: 'GET', pattern: 'auth/sessions', handler: () => ok(getState().sessions) },
  {
    method: 'DELETE',
    pattern: 'auth/sessions',
    handler: () => {
      const s = getState()
      const before = s.sessions.length
      s.sessions = s.sessions.filter((x) => x.is_current)
      return ok({ revoked: before - s.sessions.length })
    },
  },
  {
    method: 'DELETE',
    pattern: 'auth/sessions/:id',
    handler: (ctx) => {
      const s = getState()
      const before = s.sessions.length
      s.sessions = s.sessions.filter((x) => x.id !== ctx.params.id)
      return ok({ revoked: before - s.sessions.length })
    },
  },

  { method: 'GET', pattern: 'auth/api-tokens', handler: () => ok(getState().apiTokens) },
  {
    method: 'POST',
    pattern: 'auth/api-tokens',
    handler: (ctx) => {
      const body = (ctx.body ?? {}) as { name?: string; expires_in_days?: number }
      const days = body.expires_in_days ?? 0
      const token = {
        id: uid('tok'),
        name: body.name ?? 'token',
        last_used_at: null,
        created_at: nowISO(),
        expires_at: days > 0 ? daysAgoISO(-days) : null,
      }
      getState().apiTokens.push(token)
      return created({ ...token, token: `vac_${fakeSha()}` })
    },
  },
  {
    method: 'DELETE',
    pattern: 'auth/api-tokens/:id',
    handler: (ctx) => {
      const s = getState()
      const before = s.apiTokens.length
      s.apiTokens = s.apiTokens.filter((t) => t.id !== ctx.params.id)
      return ok({ revoked: before - s.apiTokens.length })
    },
  },

  // ── Instance / host / notifications ─────────────────────────────────────────
  { method: 'GET', pattern: 'host/stats', handler: () => ok(hostStats()) },
  { method: 'GET', pattern: 'instance/info', handler: () => ok(getState().instance) },
  {
    method: 'GET',
    pattern: 'instance/base-domain',
    handler: () => ok({ base_domain: getState().baseDomain, effective: getState().baseDomain }),
  },
  {
    method: 'PUT',
    pattern: 'instance/base-domain',
    handler: (ctx) => {
      const body = (ctx.body ?? {}) as { base_domain?: string }
      getState().baseDomain = body.base_domain ?? ''
      return ok({
        base_domain: getState().baseDomain,
        effective: getState().baseDomain || 'apps.example.com',
      })
    },
  },
  {
    method: 'GET',
    pattern: 'instance/dns-check',
    handler: (ctx) => {
      const host = ctx.query.get('host') ?? ''
      return ok({ host, ip: '203.0.113.7', resolved: ['203.0.113.7'], points_here: true })
    },
  },
  {
    method: 'GET',
    pattern: 'instance/onboarding',
    handler: () => ok({ dismissed: getState().onboardingDismissed }),
  },
  {
    method: 'POST',
    pattern: 'instance/onboarding/dismiss',
    handler: () => {
      getState().onboardingDismissed = true
      return ok({ dismissed: true })
    },
  },
  {
    method: 'POST',
    pattern: 'instance/restart-control-plane',
    handler: () => ok({ status: 'restarting' }),
  },
  {
    method: 'POST',
    pattern: 'instance/stop-all-apps',
    handler: () => {
      let stopped = 0
      for (const app of getState().apps) {
        if (app.status !== 'stopped') {
          setStackState(app, 'stop')
          stopped += 1
        }
      }
      return ok({ stopped, failed: 0 })
    },
  },
  {
    method: 'POST',
    pattern: 'instance/reset',
    handler: () => {
      const removed = getState().apps.length
      getState().apps = []
      return ok({ removed, failed: 0 })
    },
  },

  { method: 'GET', pattern: 'settings/notifications', handler: () => ok(getState().notifications) },
  {
    method: 'PUT',
    pattern: 'settings/notifications',
    handler: (ctx) => {
      const body = (ctx.body ?? {}) as {
        discord_url?: string | null
        slack_url?: string | null
        events?: Record<string, boolean>
      }
      const n = getState().notifications
      if (body.discord_url !== undefined) n.discord_configured = !!body.discord_url
      if (body.slack_url !== undefined) n.slack_configured = !!body.slack_url
      if (body.events) n.events = body.events
      return ok({ status: 'ok' })
    },
  },
  { method: 'POST', pattern: 'settings/notifications/test', handler: () => ok({ sent: 1 }) },

  // ── Activity feed / curated revert (plan 11) ────────────────────────────────
  {
    method: 'GET',
    pattern: 'audit',
    handler: (ctx) => ok(listAudit(Number(ctx.query.get('limit') ?? '100'))),
  },
  {
    method: 'POST',
    pattern: 'audit/:id/revert',
    handler: (ctx) => {
      const res = revertAudit(ctx.params.id ?? '')
      switch (res.status) {
        case 'ok':
          return ok({ reverted: ctx.params.id, summary: res.summary })
        case 'conflict':
          throw new MockHttpError(409, 'conflict', 'already reverted')
        case 'not_revertable':
          throw new MockHttpError(422, 'not_revertable', 'this action cannot be reverted')
        default:
          throw notFound('activity entry')
      }
    },
  },

  // ── Apps ────────────────────────────────────────────────────────────────────
  { method: 'GET', pattern: 'apps', handler: () => ok(getState().apps.map(stripApp)) },
  {
    method: 'POST',
    pattern: 'apps',
    handler: (ctx) => created(stripApp(createApp((ctx.body ?? {}) as CreateAppInput))),
  },
  { method: 'GET', pattern: 'apps/:id', handler: (ctx) => ok(stripApp(appOr404(ctx))) },
  {
    method: 'PATCH',
    pattern: 'apps/:id',
    handler: (ctx) => ok(stripApp(updateApp(appOr404(ctx), (ctx.body ?? {}) as UpdateAppInput))),
  },
  {
    method: 'DELETE',
    pattern: 'apps/:id',
    handler: (ctx) => {
      appOr404(ctx)
      deleteApp(ctx.params.id ?? '')
      return ok({ deleted: 1 })
    },
  },
  {
    method: 'POST',
    pattern: 'apps/:id/start',
    handler: (ctx) => ok({ status: setStackState(appOr404(ctx), 'start') }),
  },
  {
    method: 'POST',
    pattern: 'apps/:id/stop',
    handler: (ctx) => ok({ status: setStackState(appOr404(ctx), 'stop') }),
  },
  {
    method: 'POST',
    pattern: 'apps/:id/restart',
    handler: (ctx) => ok({ status: setStackState(appOr404(ctx), 'restart') }),
  },
  {
    method: 'POST',
    pattern: 'apps/:id/test-connection',
    handler: (ctx) => (appOr404(ctx), ok({ ok: true })),
  },
  {
    method: 'GET',
    pattern: 'apps/:id/ssh-key',
    handler: (ctx) => ok(sshKeyFor(appOr404(ctx).slug)),
  },
  {
    method: 'POST',
    pattern: 'apps/:id/ssh-key/regenerate',
    handler: (ctx) => ok(sshKeyFor(appOr404(ctx).slug, true)),
  },

  // ── Services ──────────────────────────────────────────────────────────────
  { method: 'GET', pattern: 'apps/:id/services', handler: (ctx) => ok(appOr404(ctx).services) },
  {
    method: 'PATCH',
    pattern: 'apps/:id/services/:name',
    handler: (ctx) => {
      const s = updateService(
        appOr404(ctx),
        ctx.params.name ?? '',
        (ctx.body ?? {}) as Record<string, never>,
      )
      if (!s) throw notFound('service')
      return ok(s)
    },
  },
  {
    method: 'POST',
    pattern: 'apps/:id/services/:name/restart',
    handler: (ctx) => {
      const s = restartService(appOr404(ctx), ctx.params.name ?? '')
      if (!s) throw notFound('service')
      return ok({ status: s.status })
    },
  },
  {
    method: 'GET',
    pattern: 'apps/:id/services/:name/metrics',
    handler: (ctx) => ok(appMetrics(appOr404(ctx))),
  },

  // ── Deployments ─────────────────────────────────────────────────────────────
  {
    method: 'GET',
    pattern: 'apps/:id/deployments',
    handler: (ctx) => ok(appOr404(ctx).deployments.map(toPublicDeployment)),
  },
  {
    method: 'POST',
    pattern: 'apps/:id/deployments',
    handler: (ctx) => created(triggerDeploy(appOr404(ctx))),
  },
  {
    method: 'GET',
    pattern: 'apps/:id/deployments/:did/logs',
    handler: (ctx) => {
      const app = appOr404(ctx)
      const dep = app.deployments.find((d) => d.id === ctx.params.did)
      if (!dep) throw notFound('deployment')
      const after = Number(ctx.query.get('after') ?? '0')
      const limit = Number(ctx.query.get('limit') ?? '500')
      return ok(dep.logs.filter((l) => l.id > after).slice(0, limit))
    },
  },
  {
    method: 'GET',
    pattern: 'apps/:id/deployments/:did',
    handler: (ctx) => {
      const app = appOr404(ctx)
      const dep = app.deployments.find((d) => d.id === ctx.params.did)
      if (!dep) throw notFound('deployment')
      return ok(toPublicDeployment(dep))
    },
  },

  // ── Env vars ──────────────────────────────────────────────────────────────
  { method: 'GET', pattern: 'apps/:id/env', handler: (ctx) => ok(listEnv(appOr404(ctx))) },
  {
    method: 'PUT',
    pattern: 'apps/:id/env',
    handler: (ctx) => {
      const body = (ctx.body ?? {}) as { vars?: EnvVarInput[] }
      return ok({ saved: replaceEnv(appOr404(ctx), body.vars ?? []) })
    },
  },
  {
    method: 'GET',
    pattern: 'apps/:id/env/:key/reveal',
    handler: (ctx) => {
      const v = revealEnv(appOr404(ctx), decodeURIComponent(ctx.params.key ?? ''))
      if (!v) throw notFound('env var')
      return ok(v)
    },
  },

  // ── Domains ───────────────────────────────────────────────────────────────
  { method: 'GET', pattern: 'apps/:id/domains', handler: (ctx) => ok(appOr404(ctx).domains) },
  {
    method: 'POST',
    pattern: 'apps/:id/services/:service/domains',
    handler: (ctx) => {
      const body = (ctx.body ?? {}) as { hostname?: string }
      return created(createDomain(appOr404(ctx), ctx.params.service ?? '', body.hostname ?? ''))
    },
  },
  {
    method: 'DELETE',
    pattern: 'apps/:id/domains/:domainId',
    handler: (ctx) => {
      if (!deleteDomain(appOr404(ctx), ctx.params.domainId ?? '')) throw notFound('domain')
      return ok({ status: 'deleted' })
    },
  },

  // ── Metrics ─────────────────────────────────────────────────────────────────
  { method: 'GET', pattern: 'apps/:id/metrics', handler: (ctx) => ok(appMetrics(appOr404(ctx))) },
]

// Drop internal record fields so responses match the public App wire shape.
function stripApp(app: import('./types').AppRecord) {
  const { services: _s, deployments: _d, env: _e, domains: _dm, ...pub } = app
  void _s
  void _d
  void _e
  void _dm
  return pub
}

function sshKeyFor(slug: string, regenerated = false) {
  const fp = fakeSha()
    .slice(0, 32)
    .replace(/(.{2})(?=.)/g, '$1:')
  return {
    public_key: `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI${fakeSha().slice(0, 32)} vac-${slug}`,
    fingerprint: `SHA256:${fp}`,
    created_at: regenerated ? nowISO() : daysAgoISO(11),
  }
}

// ── Matcher ───────────────────────────────────────────────────────────────────

function matchPattern(pattern: string, path: string): Record<string, string> | null {
  const pSeg = pattern.split('/')
  const aSeg = path.split('/')
  if (pSeg.length !== aSeg.length) return null
  const params: Record<string, string> = {}
  for (let i = 0; i < pSeg.length; i += 1) {
    const p = pSeg[i]!
    const a = aSeg[i]!
    if (p.startsWith(':')) params[p.slice(1)] = decodeURIComponent(a)
    else if (p !== a) return null
  }
  return params
}

export interface ResolvedResponse {
  status: number
  body: unknown
}

// Resolve a request against the route table. `apiPath` is the path with the
// leading `/api/` stripped and without query string.
export async function handleRequest(
  method: string,
  apiPath: string,
  query: URLSearchParams,
  body: unknown,
): Promise<ResolvedResponse> {
  for (const route of routes) {
    if (route.method !== method) continue
    const params = matchPattern(route.pattern, apiPath)
    if (!params) continue
    const result = await route.handler({ params, query, body })
    return { status: result.status ?? (result.body === undefined ? 204 : 200), body: result.body }
  }
  throw notFound(`${method} /api/${apiPath}`)
}
