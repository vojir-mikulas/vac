import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { handleRequest } from './handlers'
import { resetState } from './db'
import type { App, Deployment, EnvVar } from '@/types/api'
import type { AuditEntry } from '@/lib/api/audit'

const q = () => new URLSearchParams()

async function call(method: string, path: string, body?: unknown) {
  return handleRequest(method, path, q(), body)
}

describe('mock backend handlers', () => {
  beforeEach(() => resetState())

  it('serves the seeded app list and detail', async () => {
    const list = await call('GET', 'apps')
    const apps = list.body as App[]
    expect(apps.length).toBe(4)
    expect(apps.map((a) => a.slug)).toContain('storefront')

    const detail = await call('GET', 'apps/storefront')
    expect((detail.body as App).name).toBe('Storefront')
  })

  it('404s unknown apps with the shared error shape', async () => {
    await expect(call('GET', 'apps/does-not-exist')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    })
  })

  it('hides sensitive env values until revealed', async () => {
    const list = await call('GET', 'apps/storefront/env')
    const vars = list.body as EnvVar[]
    const secret = vars.find((v) => v.key === 'DATABASE_URL')
    expect(secret?.sensitive).toBe(true)
    expect(secret?.value).toBeUndefined()

    const revealed = await call('GET', 'apps/storefront/env/DATABASE_URL/reveal')
    expect((revealed.body as EnvVar).value).toMatch(/postgres:/)
  })

  it('start/stop flips stack and service status', async () => {
    const stopped = await call('POST', 'apps/storefront/stop')
    expect((stopped.body as { status: string }).status).toBe('stopped')
    const afterStop = await call('GET', 'apps/storefront')
    expect((afterStop.body as App).status).toBe('stopped')

    await call('POST', 'apps/storefront/start')
    const afterStart = await call('GET', 'apps/storefront')
    expect((afterStart.body as App).status).toBe('running')
  })

  it('creates and deletes apps', async () => {
    const res = await call('POST', 'apps', {
      name: 'My New App',
      git_url: 'git@github.com:me/x.git',
    })
    expect(res.status).toBe(201)
    const app = res.body as App
    expect(app.slug).toBe('my-new-app')

    const list = (await call('GET', 'apps')).body as App[]
    expect(list.length).toBe(5)

    await call('DELETE', `apps/${app.id}`)
    expect(((await call('GET', 'apps')).body as App[]).length).toBe(4)
  })
})

describe('activity feed + revert', () => {
  beforeEach(() => resetState())

  it('serves the seeded audit log newest-first', async () => {
    const rows = (await call('GET', 'audit')).body as AuditEntry[]
    expect(rows.length).toBeGreaterThan(0)
    for (let i = 1; i < rows.length; i += 1) {
      expect(new Date(rows[i - 1]!.created_at).getTime()).toBeGreaterThanOrEqual(
        new Date(rows[i]!.created_at).getTime(),
      )
    }
  })

  it('reverts a revertable entry once, then 409s', async () => {
    const rows = (await call('GET', 'audit')).body as AuditEntry[]
    const target = rows.find((r) => r.revertable)!
    expect(target).toBeDefined()

    const res = await call('POST', `audit/${target.id}/revert`)
    expect((res.body as { reverted: string }).reverted).toBe(target.id)

    // The entry is now marked reverted, and a revert entry was appended.
    const after = (await call('GET', 'audit')).body as AuditEntry[]
    expect(after.find((r) => r.id === target.id)?.reverted_at).toBeTruthy()
    expect(after.some((r) => r.action.includes('/revert'))).toBe(true)

    await expect(call('POST', `audit/${target.id}/revert`)).rejects.toMatchObject({
      status: 409,
      code: 'conflict',
    })
  })

  it('422s a non-revertable entry', async () => {
    const rows = (await call('GET', 'audit')).body as AuditEntry[]
    const target = rows.find((r) => !r.revertable && !r.reverted_at)!
    await expect(call('POST', `audit/${target.id}/revert`)).rejects.toMatchObject({
      status: 422,
      code: 'not_revertable',
    })
  })

  it('persists onboarding dismissal', async () => {
    expect((await call('GET', 'instance/onboarding')).body).toMatchObject({ dismissed: false })
    expect((await call('POST', 'instance/onboarding/dismiss')).body).toMatchObject({
      dismissed: true,
    })
    expect((await call('GET', 'instance/onboarding')).body).toMatchObject({ dismissed: true })
  })
})

describe('deploy lifecycle', () => {
  beforeEach(() => {
    resetState()
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  it('advances a triggered deploy through to running on timers', async () => {
    const res = await call('POST', 'apps/analytics/deployments')
    const dep = res.body as Deployment
    expect(dep.status).toBe('queued')

    // Drive every scheduled timer to completion.
    await vi.runAllTimersAsync()

    const list = (await call('GET', 'apps/analytics/deployments')).body as Deployment[]
    const advanced = list.find((d) => d.id === dep.id)
    expect(advanced?.status).toBe('running')
    expect(advanced?.finished_at).not.toBeNull()

    const app = (await call('GET', 'apps/analytics')).body as App
    expect(app.status).toBe('running')

    // Build logs accumulated and are retrievable via the REST endpoint.
    const logs = (await call('GET', `apps/analytics/deployments/${dep.id}/logs`)).body as unknown[]
    expect(logs.length).toBeGreaterThan(0)
  })
})
