// Central query-key factory. Keep keys here so invalidation stays consistent.

export const queryKeys = {
  auth: {
    me: ['auth', 'me'] as const,
    sessions: ['auth', 'sessions'] as const,
    apiTokens: ['auth', 'api-tokens'] as const,
  },
  setup: {
    status: ['setup', 'status'] as const,
  },
  host: {
    stats: ['host', 'stats'] as const,
    budget: ['host', 'budget'] as const,
  },
  notifications: ['notifications'] as const,
  activity: ['activity'] as const,
  domains: ['domains'] as const,
  instance: {
    info: ['instance', 'info'] as const,
    baseDomain: ['instance', 'base-domain'] as const,
    onboarding: ['instance', 'onboarding'] as const,
  },
  apps: {
    all: ['apps'] as const,
    detail: (id: string) => ['apps', id] as const,
    services: (id: string) => ['apps', id, 'services'] as const,
    deployments: (id: string) => ['apps', id, 'deployments'] as const,
    deployment: (id: string, did: string) => ['apps', id, 'deployments', did] as const,
    env: (id: string) => ['apps', id, 'env'] as const,
    domains: (id: string) => ['apps', id, 'domains'] as const,
    sshKey: (id: string) => ['apps', id, 'ssh-key'] as const,
    triggers: (id: string) => ['apps', id, 'triggers'] as const,
    webhook: (id: string) => ['apps', id, 'webhook'] as const,
    metrics: (id: string, since: string) => ['apps', id, 'metrics', since] as const,
    // Track D — managed services.
    backups: (id: string) => ['apps', id, 'backups'] as const,
    backupRuns: (id: string, cid: string) => ['apps', id, 'backups', cid, 'runs'] as const,
    databases: (id: string) => ['apps', id, 'databases'] as const,
  },
  addons: {
    all: ['addons'] as const,
    detail: (id: string) => ['addons', id] as const,
  },
} as const
