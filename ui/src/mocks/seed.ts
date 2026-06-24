// Initial demo data for the mock backend. Tuned to make the dashboard look
// populated and varied on first load: a healthy multi-service app, a static
// site, a degraded app, and a stopped one — plus deploy history including a
// failed deploy so error states are visible without triggering one.

import type {
  AppStatus,
  DeploymentLogLine,
  DeploymentStatus,
  Service,
  ServiceStatus,
} from '@/types/api'
import type { AppRecord, DeployRecord, EnvRecord, MockState } from './types'
import { daysAgoISO, fakeSha, minutesAgoISO, uid } from './util'

function svc(
  appId: string,
  name: string,
  status: ServiceStatus,
  internalPort: number | null,
  opts: {
    restartCount?: number
    lastExit?: number | null
    healthPath?: string | null
    hasVolumes?: boolean
    isPrivate?: boolean
  } = {},
): Service {
  return {
    id: uid('svc'),
    app_id: appId,
    name,
    container_id: status === 'stopped' ? null : uid('c').replace('c_', ''),
    exposed_port: null,
    internal_port: internalPort,
    health_path: opts.healthPath ?? (internalPort ? '/' : null),
    status,
    restart_count: opts.restartCount ?? 0,
    last_exit_code: opts.lastExit ?? null,
    oom_killed_count: 0,
    has_volumes: opts.hasVolumes ?? false,
    is_private: opts.isPrivate ?? false,
    requires_auth: false,
    guest_access_enabled: false,
    created_at: daysAgoISO(12),
    updated_at: minutesAgoISO(3),
  }
}

function logLine(
  id: number,
  service: string | null,
  stream: string,
  message: string,
  ago: number,
): DeploymentLogLine {
  return { id, service_name: service, stream, message, ts: minutesAgoISO(ago) }
}

function deployment(
  appId: string,
  status: DeploymentStatus,
  minsAgo: number,
  message: string,
  logs: DeploymentLogLine[],
  error: string | null = null,
): DeployRecord {
  const triggered = minutesAgoISO(minsAgo)
  return {
    id: uid('dep'),
    app_id: appId,
    status,
    triggered_at: triggered,
    triggered_by: 'manual',
    rolled_back_from: null,
    started_at: minutesAgoISO(minsAgo),
    finished_at: minutesAgoISO(minsAgo - 1),
    compose_hash: fakeSha().slice(0, 12),
    commit_sha: fakeSha(),
    commit_message: message,
    error,
    logs,
  }
}

function successLogs(slug: string): DeploymentLogLine[] {
  return [
    logLine(1, null, 'stdout', `Cloning repository (branch main)…`, 9),
    logLine(2, null, 'stdout', 'HEAD is now at 9f3c2a1', 9),
    logLine(3, null, 'stdout', '#1 [internal] load build definition', 8),
    logLine(4, null, 'stdout', '#5 [build 2/5] RUN npm ci', 8),
    logLine(5, null, 'stdout', 'added 412 packages in 7s', 8),
    logLine(6, null, 'stdout', '#8 exporting layers done', 7),
    logLine(7, null, 'stdout', `Creating ${slug}--web…`, 7),
    logLine(8, null, 'stdout', `Caddy reports ${slug}--web healthy`, 6),
    logLine(9, null, 'stdout', 'Deployment live ✓', 6),
  ]
}

function failedLogs(slug: string): DeploymentLogLine[] {
  return [
    logLine(1, null, 'stdout', 'Cloning repository (branch main)…', 70),
    logLine(2, null, 'stdout', '#5 [build 2/5] RUN npm run build', 69),
    logLine(
      3,
      null,
      'stderr',
      'src/index.ts(12,7): error TS2304: Cannot find name "createServer".',
      69,
    ),
    logLine(4, null, 'stderr', 'npm ERR! code ELIFECYCLE', 69),
    logLine(5, null, 'stderr', `build of ${slug}--web failed with exit code 1`, 69),
  ]
}

function app(
  partial: Pick<AppRecord, 'name' | 'slug' | 'git_url' | 'status'> &
    Partial<AppRecord> & { services: Service[]; env: EnvRecord[]; deployments: DeployRecord[] },
): AppRecord {
  return {
    id: partial.id ?? uid('app'),
    name: partial.name,
    slug: partial.slug,
    git_url: partial.git_url,
    git_branch: partial.git_branch ?? 'main',
    compose_file: partial.compose_file ?? 'compose.yaml',
    build_kind: partial.build_kind ?? 'compose',
    build_config: partial.build_config ?? {},
    status: partial.status,
    mem_limit_mb: partial.mem_limit_mb ?? null,
    disk_limit_mb: partial.disk_limit_mb ?? null,
    created_at: partial.created_at ?? daysAgoISO(12),
    updated_at: partial.updated_at ?? minutesAgoISO(6),
    source: partial.source ?? 'git',
    template_id: partial.template_id ?? null,
    is_preview: partial.is_preview ?? false,
    maintenance_mode: partial.maintenance_mode ?? false,
    maintenance_auto: partial.maintenance_auto ?? false,
    maintenance_active: partial.maintenance_active ?? false,
    idle_suspend_enabled: partial.idle_suspend_enabled ?? false,
    idle_timeout_minutes: partial.idle_timeout_minutes ?? null,
    suspended: partial.suspended ?? false,
    last_traffic_at: partial.last_traffic_at,
    services: partial.services,
    deployments: partial.deployments,
    env: partial.env,
    domains: partial.domains ?? [],
  }
}

export function buildInitialState(): MockState {
  // ── App 1: Storefront — healthy multi-service compose stack ───────────────
  const a1 = uid('app')
  const storefront = app({
    id: a1,
    name: 'Storefront',
    slug: 'storefront',
    git_url: 'git@github.com:acme/storefront.git',
    status: 'running' satisfies AppStatus,
    build_kind: 'compose',
    services: [
      svc(a1, 'web', 'running', 3000),
      svc(a1, 'worker', 'running', null),
      svc(a1, 'redis', 'running', 6379, { healthPath: null, hasVolumes: true }),
    ],
    env: [
      { key: 'NODE_ENV', value: 'production', sensitive: false },
      { key: 'PORT', value: '3000', sensitive: false },
      { key: 'DATABASE_URL', value: 'postgres://app:s3cr3t@db:5432/store', sensitive: true },
      { key: 'STRIPE_SECRET_KEY', value: 'sk_live_REDACTED', sensitive: true },
    ],
    deployments: [
      deployment(a1, 'running', 6, 'feat: add wishlist sharing', successLogs('storefront')),
      deployment(a1, 'running', 240, 'fix: cart total rounding', successLogs('storefront')),
      deployment(a1, 'running', 1500, 'chore: bump deps', successLogs('storefront')),
    ],
    domains: [
      {
        id: uid('dom'),
        app_id: a1,
        service_name: 'web',
        hostname: 'shop.example.com',
        type: 'custom',
        managed: false,
        status: 'active',
        created_at: daysAgoISO(11),
        updated_at: daysAgoISO(11),
      },
    ],
  })

  // ── App 2: Marketing Site — static build ──────────────────────────────────
  const a2 = uid('app')
  const marketing = app({
    id: a2,
    name: 'Marketing Site',
    slug: 'marketing',
    git_url: 'https://github.com/acme/marketing.git',
    status: 'running',
    build_kind: 'static',
    build_config: { staticDir: 'dist', spaFallback: true },
    compose_file: '',
    services: [svc(a2, 'web', 'running', 80)],
    env: [{ key: 'VITE_API_BASE', value: 'https://shop.example.com/api', sensitive: false }],
    deployments: [
      deployment(a2, 'running', 55, 'content: spring campaign hero', successLogs('marketing')),
    ],
    domains: [
      {
        id: uid('dom'),
        app_id: a2,
        service_name: 'web',
        hostname: 'www.example.com',
        type: 'custom',
        managed: false,
        status: 'active',
        created_at: daysAgoISO(9),
        updated_at: daysAgoISO(9),
      },
    ],
  })

  // ── App 3: API Gateway — degraded (a service is crash-looping) ─────────────
  const a3 = uid('app')
  const gateway = app({
    id: a3,
    name: 'API Gateway',
    slug: 'api-gateway',
    git_url: 'git@github.com:acme/api-gateway.git',
    status: 'degraded',
    build_kind: 'dockerfile',
    build_config: { dockerfilePath: 'Dockerfile' },
    compose_file: '',
    services: [
      svc(a3, 'gateway', 'running', 8080),
      svc(a3, 'auth', 'crashed', 9000, { restartCount: 4, lastExit: 1 }),
    ],
    env: [
      { key: 'UPSTREAM_TIMEOUT', value: '30s', sensitive: false },
      { key: 'JWT_SIGNING_KEY', value: 'hS8fJ2kLpQwErTyUiOpAsDfGhJkL', sensitive: true },
    ],
    deployments: [
      deployment(a3, 'running', 30, 'feat: rate-limit per token', successLogs('api-gateway')),
      deployment(
        a3,
        'error',
        69,
        'refactor: extract auth middleware',
        failedLogs('api-gateway'),
        'build of api-gateway--auth failed with exit code 1',
      ),
    ],
  })

  // ── App 4: Analytics — stopped React app ──────────────────────────────────
  const a4 = uid('app')
  const analytics = app({
    id: a4,
    name: 'Analytics',
    slug: 'analytics',
    git_url: 'git@github.com:acme/analytics.git',
    status: 'stopped',
    build_kind: 'framework',
    build_config: {
      framework: 'react',
      buildCommand: 'npm run build',
      startCommand: '',
      port: 4173,
    },
    compose_file: '',
    services: [svc(a4, 'web', 'stopped', 4173)],
    env: [{ key: 'VITE_POSTHOG_KEY', value: 'phc_EXAMPLEanalyticskey', sensitive: true }],
    deployments: [
      deployment(a4, 'running', 4320, 'init: dashboard scaffold', successLogs('analytics')),
    ],
  })

  const now = new Date().toISOString()

  return {
    user: { id: uid('usr'), username: 'admin', totp_enabled: false },
    sessions: [
      {
        id: uid('ses'),
        ip: '203.0.113.7',
        user_agent: 'Mozilla/5.0 (Macintosh) Chrome/120',
        created_at: daysAgoISO(2),
        last_seen_at: now,
        expires_at: daysAgoISO(-28),
        is_current: true,
      },
      {
        id: uid('ses'),
        ip: '198.51.100.42',
        user_agent: 'Mozilla/5.0 (iPhone) Safari/17',
        created_at: daysAgoISO(5),
        last_seen_at: daysAgoISO(1),
        expires_at: daysAgoISO(-25),
        is_current: false,
      },
    ],
    apiTokens: [
      {
        id: uid('tok'),
        name: 'ci-deploy',
        last_used_at: minutesAgoISO(180),
        created_at: daysAgoISO(20),
        expires_at: null,
      },
    ],
    notifications: {
      discord_configured: true,
      discord_hint: 'https://discord.com/api/webhooks/****/****',
      slack_configured: false,
      slack_hint: '',
      smtp_host: '',
      smtp_port: 0,
      smtp_username: '',
      smtp_from: '',
      smtp_to: '',
      smtp_tls_mode: 'starttls',
      smtp_password_configured: false,
      smtp_password_hint: '',
      events: {
        'deploy.succeeded': true,
        'deploy.failed': true,
        'app.crashed': true,
        'app.recovered': false,
      },
    },
    instance: {
      version: '0.4.0-preview',
      commit: fakeSha().slice(0, 7),
      built_at: daysAgoISO(1),
      channel: 'preview',
      managed_services: true,
      enable_shell: true,
      idle_suspend: true,
    },
    baseDomain: 'apps.example.com',
    apps: [storefront, marketing, gateway, analytics],
    audit: buildAuditSeed(storefront, marketing, gateway),
    onboardingDismissed: false,
  }
}

// buildAuditSeed produces a small, realistic activity feed: a mix of actors and
// actions, with a few revertable config changes (one already reverted) so the
// Activity page demonstrates revert in both states.
function buildAuditSeed(
  storefront: AppRecord,
  marketing: AppRecord,
  gateway: AppRecord,
): MockState['audit'] {
  return [
    {
      id: uid('aud'),
      actor_type: 'user',
      actor: 'admin',
      action: 'PUT /api/apps/{id}/env',
      target_type: 'app',
      target_id: storefront.id,
      summary: `replaced environment for ${storefront.slug}`,
      status_code: 200,
      revertable: true,
      has_preview: true,
      created_at: minutesAgoISO(14),
    },
    {
      id: uid('aud'),
      actor_type: 'user',
      actor: 'admin',
      action: 'PATCH /api/apps/{id}',
      target_type: 'app',
      target_id: marketing.id,
      summary: `updated configuration for ${marketing.slug}`,
      status_code: 200,
      revertable: true,
      has_preview: true,
      created_at: minutesAgoISO(42),
    },
    {
      id: uid('aud'),
      actor_type: 'api_token',
      actor: 'ci-deploy',
      action: 'POST /api/apps/{id}/deployments',
      target_type: 'app',
      target_id: gateway.id,
      summary: `triggered deployment of ${gateway.slug}`,
      status_code: 202,
      revertable: false,
      has_preview: false,
      created_at: minutesAgoISO(67),
    },
    {
      id: uid('aud'),
      actor_type: 'user',
      actor: 'admin',
      action: 'PUT /api/instance/base-domain',
      summary: 'set base domain to apps.example.com',
      status_code: 200,
      revertable: false,
      // Already reverted: the Revert button is gone, but Preview stays — a
      // reverted entry is still inspectable (plan 22).
      has_preview: true,
      reverted_at: minutesAgoISO(120),
      created_at: minutesAgoISO(130),
    },
    {
      id: uid('aud'),
      actor_type: 'system',
      actor: '',
      action: 'POST /api/apps/{id}/deployments',
      target_type: 'app',
      target_id: storefront.id,
      summary: `auto-deployed ${storefront.slug} from push to main`,
      status_code: 202,
      revertable: false,
      has_preview: false,
      created_at: minutesAgoISO(200),
    },
    {
      id: uid('aud'),
      actor_type: 'anonymous',
      actor: '',
      action: 'POST /api/auth/login',
      summary: undefined,
      status_code: 401,
      revertable: false,
      has_preview: false,
      created_at: minutesAgoISO(305),
    },
  ]
}
