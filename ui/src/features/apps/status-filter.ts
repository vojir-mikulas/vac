import type { App } from '@/types/api'

export type AppFilter = 'all' | 'running' | 'issues' | 'stopped'

const ISSUE_STATUSES = new Set(['crashed', 'degraded', 'failed', 'interrupted'])

export function matchesFilter(app: App, filter: AppFilter): boolean {
  switch (filter) {
    case 'running':
      return app.status === 'running'
    case 'issues':
      return ISSUE_STATUSES.has(app.status)
    case 'stopped':
      return app.status === 'stopped'
    default:
      return true
  }
}

export function countByFilter(apps: App[]) {
  let running = 0
  let issues = 0
  let stopped = 0
  for (const app of apps) {
    if (app.status === 'running') running++
    else if (ISSUE_STATUSES.has(app.status)) issues++
    else if (app.status === 'stopped') stopped++
  }
  return { all: apps.length, running, issues, stopped }
}
