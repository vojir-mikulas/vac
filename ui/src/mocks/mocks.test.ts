import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { handleRequest } from './handlers'
import { resetState } from './db'
import type { App, Deployment, EnvVar } from '@/types/api'

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
