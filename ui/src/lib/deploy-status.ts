// Deployment-status semantics, kept in one place so the UI agrees with the
// backend enum (api/internal/deploy/status.go). The backend's terminal states
// are `running` (succeeded), `error` (failed), and `interrupted` (process
// restarted or reaped mid-deploy). Legacy `success`/`failed` strings are
// accepted as aliases so older rows / external callers still classify.

export const DEPLOY_ACTIVE_STATUSES = [
  'queued',
  'cloning',
  'building',
  'deploying',
  'health-checking',
] as const

export function isDeployActive(status: string): boolean {
  return (DEPLOY_ACTIVE_STATUSES as readonly string[]).includes(status)
}

export function isDeploySucceeded(status: string): boolean {
  return status === 'running' || status === 'success'
}

export function isDeployFailed(status: string): boolean {
  return status === 'error' || status === 'failed' || status === 'interrupted'
}

// User-initiated cancellation — distinct from `interrupted` (process restart).
export function isDeployCanceled(status: string): boolean {
  return status === 'canceled'
}

export function isDeployTerminal(status: string): boolean {
  return isDeploySucceeded(status) || isDeployFailed(status) || isDeployCanceled(status)
}
