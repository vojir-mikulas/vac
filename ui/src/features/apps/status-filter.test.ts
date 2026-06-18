import { describe, expect, it } from 'vitest'

import { appsBadgeVariant, countByFilter, matchesFilter } from '@/features/apps/status-filter'
import type { App } from '@/types/api'

function app(status: string): App {
  return {
    id: status,
    name: status,
    slug: status,
    git_url: 'git@x:y.git',
    git_branch: 'main',
    compose_file: 'compose.yaml',
    build_kind: 'auto',
    build_config: {},
    status,
    mem_limit_mb: null,
    disk_limit_mb: null,
    created_at: '',
    updated_at: '',
    source: 'git',
    template_id: null,
    is_preview: false,
    maintenance_mode: false,
    maintenance_auto: false,
    maintenance_active: false,
    idle_suspend_enabled: false,
    idle_timeout_minutes: null,
    suspended: false,
  }
}

const apps = [app('running'), app('running'), app('crashed'), app('stopped')]

describe('matchesFilter', () => {
  it('matches by bucket', () => {
    expect(matchesFilter(app('running'), 'running')).toBe(true)
    expect(matchesFilter(app('crashed'), 'issues')).toBe(true)
    expect(matchesFilter(app('degraded'), 'issues')).toBe(true)
    expect(matchesFilter(app('stopped'), 'running')).toBe(false)
    expect(matchesFilter(app('stopped'), 'all')).toBe(true)
  })
})

describe('countByFilter', () => {
  it('tallies each bucket', () => {
    expect(countByFilter(apps)).toEqual({
      all: 4,
      running: 2,
      issues: 1,
      stopped: 1,
    })
  })
})

describe('appsBadgeVariant', () => {
  it('is green when every app is up', () => {
    expect(appsBadgeVariant({ all: 2, running: 2, issues: 0 })).toBe('success')
  })

  it('is amber when something is broken', () => {
    expect(appsBadgeVariant({ all: 2, running: 1, issues: 1 })).toBe('warn')
  })

  it('is neutral when apps are merely stopped', () => {
    expect(appsBadgeVariant({ all: 2, running: 1, issues: 0 })).toBe('secondary')
  })

  it('is neutral when there are no apps', () => {
    expect(appsBadgeVariant({ all: 0, running: 0, issues: 0 })).toBe('secondary')
  })
})
