// Internal shapes for the mock store. These extend the public API types with
// the bits a real backend keeps server-side (raw secret values, per-deployment
// log buffers) that the wire types deliberately omit.

import type {
  ApiToken,
  App,
  Deployment,
  DeploymentLogLine,
  Domain,
  NotificationSettings,
  Service,
  Session,
  User,
} from '@/types/api'
import type { InstanceInfo } from '@/lib/api/instance'
import type { AuditEntry } from '@/lib/api/audit'

export interface EnvRecord {
  key: string
  value: string
  sensitive: boolean
}

export interface DeployRecord extends Deployment {
  logs: DeploymentLogLine[]
}

export interface AppRecord extends App {
  services: Service[]
  deployments: DeployRecord[]
  env: EnvRecord[]
  domains: Domain[]
  /** Custom maintenance-page HTML; null/undefined = built-in default. */
  maintenance_html?: string | null
  /** Deploy-window schedule (Phase 3); undefined = always allowed. */
  deploy_window?: import('@/lib/api/deploy-window').DeployWindow[]
}

export interface MockState {
  user: User
  sessions: Session[]
  apiTokens: ApiToken[]
  notifications: NotificationSettings
  instance: InstanceInfo
  baseDomain: string
  apps: AppRecord[]
  audit: AuditEntry[]
  onboardingDismissed: boolean
}
