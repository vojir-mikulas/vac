import { describe, expect, it, vi } from 'vitest'
import type { TFunction } from 'i18next'

import type { App } from '@/types/api'
import { navItems } from '@/lib/navigation'
import {
  actionCommands,
  appContextCommands,
  appListCommands,
  appTabPaths,
  navCommands,
  settingsCommands,
  type Navigate,
} from './commands'

// Echoes the key back so assertions can read labels without the catalog.
const t = ((key: string) => key) as unknown as TFunction
const noopGo: Navigate = () => {}

function makeApp(overrides: Partial<App> = {}): App {
  return {
    id: 'app-1',
    name: 'Acme',
    slug: 'acme',
    git_url: 'git@example.com:acme.git',
    git_branch: 'main',
    compose_file: 'compose.yaml',
    build_kind: 'compose',
    build_config: {},
    status: 'running',
    mem_limit_mb: null,
    disk_limit_mb: null,
    created_at: '',
    updated_at: '',
    source: 'git',
    template_id: null,
    is_preview: false,
    parent_app_id: undefined,
    maintenance_mode: false,
    maintenance_auto: false,
    maintenance_active: false,
    idle_suspend_enabled: false,
    idle_timeout_minutes: null,
    suspended: false,
    ...overrides,
  } as App
}

describe('navCommands', () => {
  it('emits one navigate command per nav entry, including the managed surfaces', () => {
    const base = navCommands(navItems(false), t, noopGo)
    const managed = navCommands(navItems(true), t, noopGo)

    expect(base.every((c) => c.group === 'navigate')).toBe(true)
    expect(base.map((c) => c.id)).toContain('nav:/logs')
    expect(base.map((c) => c.id)).toContain('nav:/security')
    expect(base.map((c) => c.id)).toContain('nav:/storage')
    // Managed-services surfaces only appear when the gate is open.
    expect(base.map((c) => c.id)).not.toContain('nav:/addons')
    expect(managed.map((c) => c.id)).toContain('nav:/addons')
    expect(managed.map((c) => c.id)).toContain('nav:/backups')
    expect(managed.length).toBe(base.length + 2)
  })

  it('drift guard: every nav source entry produces a nav command', () => {
    for (const managed of [false, true]) {
      const items = navItems(managed)
      const cmds = navCommands(items, t, noopGo)
      expect(cmds).toHaveLength(items.length)
      for (const item of items) {
        expect(cmds.map((c) => c.id)).toContain(`nav:${item.to}`)
      }
    }
  })

  it('navigates on perform', () => {
    const go = vi.fn()
    navCommands(navItems(false), t, go)[0]!.perform()
    expect(go).toHaveBeenCalledWith('/apps')
  })
})

describe('settingsCommands', () => {
  it('lists all eight settings subpages', () => {
    const cmds = settingsCommands(t, noopGo)
    expect(cmds).toHaveLength(8)
    expect(cmds.every((c) => c.group === 'settings')).toBe(true)
    expect(cmds.map((c) => c.id)).toContain('settings:/settings/danger')
  })
})

describe('actionCommands', () => {
  const deps = {
    t,
    go: noopGo,
    toggleTheme: vi.fn(),
    restartControlPlane: vi.fn(),
    stopAllApps: vi.fn(),
  }

  it('gates destructive actions behind a confirm, leaves benign ones unguarded', () => {
    const cmds = actionCommands(deps)
    const find = (id: string) => cmds.find((c) => c.id === id)!
    expect(find('action:restart-control-plane').confirm).toBeDefined()
    expect(find('action:stop-all-apps').confirm).toBeDefined()
    expect(find('action:toggle-theme').confirm).toBeUndefined()
    expect(find('action:new-app').confirm).toBeUndefined()
  })

  it('wires perform to the injected callbacks', () => {
    const cmds = actionCommands(deps)
    cmds.find((c) => c.id === 'action:toggle-theme')!.perform()
    expect(deps.toggleTheme).toHaveBeenCalled()
  })
})

describe('appListCommands', () => {
  it('is empty without apps and maps each app otherwise', () => {
    expect(appListCommands(undefined, noopGo)).toHaveLength(0)
    const cmds = appListCommands(
      [makeApp(), makeApp({ id: 'app-2', name: 'Beta', slug: 'beta' })],
      noopGo,
    )
    expect(cmds).toHaveLength(2)
    expect(cmds[0]!.group).toBe('apps')
    expect(cmds[1]!.keywords).toContain('beta')
  })
})

describe('appTabPaths', () => {
  it('includes previews for a non-preview app and omits managed tabs when the gate is off', () => {
    const tabs = appTabPaths(makeApp(), false, false)
    expect(tabs).toContain('previews')
    expect(tabs).not.toContain('backups')
    expect(tabs).not.toContain('databases')
  })

  it('omits previews for a preview app', () => {
    expect(appTabPaths(makeApp({ is_preview: true }), false, false)).not.toContain('previews')
  })

  it('adds managed tabs before settings for a managed non-addon app', () => {
    const tabs = appTabPaths(makeApp(), true, false)
    expect(tabs).toContain('backups')
    expect(tabs).toContain('databases')
    expect(tabs.indexOf('databases')).toBeLessThan(tabs.indexOf('settings'))
  })

  it('hides managed tabs for an add-on with no managed database', () => {
    expect(appTabPaths(makeApp({ source: 'template' }), true, false)).not.toContain('backups')
    expect(appTabPaths(makeApp({ source: 'template' }), true, true)).toContain('backups')
  })
})

describe('appContextCommands', () => {
  const base = {
    managed: false,
    hasManagedDB: false,
    t,
    go: noopGo,
    deploy: vi.fn(),
    stack: vi.fn(),
  }

  it('offers deploy + restart (both confirmed) and stop when running', () => {
    const cmds = appContextCommands({ ...base, app: makeApp() })
    const ids = cmds.map((c) => c.id)
    expect(ids).toContain('app:deploy')
    expect(ids).toContain('app:restart')
    expect(ids).toContain('app:stop')
    expect(ids).not.toContain('app:start')
    expect(cmds.find((c) => c.id === 'app:deploy')!.confirm).toBeDefined()
    expect(cmds.find((c) => c.id === 'app:stop')!.confirm).toBeDefined()
  })

  it('offers start instead of stop when the app is stopped', () => {
    const cmds = appContextCommands({ ...base, app: makeApp({ status: 'stopped' }) })
    const ids = cmds.map((c) => c.id)
    expect(ids).toContain('app:start')
    expect(ids).not.toContain('app:stop')
    expect(cmds.find((c) => c.id === 'app:start')!.confirm).toBeUndefined()
  })

  it('wires stack actions and tab navigation through the injected callbacks', () => {
    const stack = vi.fn()
    const go = vi.fn()
    const cmds = appContextCommands({ ...base, app: makeApp(), stack, go })
    cmds.find((c) => c.id === 'app:restart')!.perform()
    expect(stack).toHaveBeenCalledWith('restart')
    cmds.find((c) => c.id === 'app:tab:overview')!.perform()
    expect(go).toHaveBeenCalledWith('/apps/$appId/overview', { appId: 'app-1' })
  })

  it('every command carries the app group', () => {
    const cmds = appContextCommands({ ...base, app: makeApp() })
    expect(cmds.every((c) => c.group === 'app')).toBe(true)
  })
})
