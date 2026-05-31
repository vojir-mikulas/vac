import { describe, expect, it } from 'vitest'

import { countByFilter, matchesFilter } from '@/features/apps/status-filter'
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
    created_at: '',
    updated_at: '',
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
