// In-memory mock store: holds all demo state, exposes getters/mutations the
// handlers call, and drives the deploy lifecycle (status transitions + live
// log frames) on wall-clock timers so the UI animates like the real thing.

import type {
  AppStatus,
  Deployment,
  DeploymentLogLine,
  DeploymentStatus,
  Domain,
  EnvVar,
  HostStats,
  MetricSample,
  Service,
  ServiceStatsData,
  WsFrame,
} from '@/types/api'
import type { CreateAppInput, UpdateAppInput } from '@/types/api'
import type { EnvVarInput } from '@/lib/api/env'
import type { ActivityDiff, AuditEntry } from '@/lib/api/audit'
import type { AppRecord, DeployRecord, MockState } from './types'
import { buildInitialState } from './seed'
import { fakeSha, nowISO, pick, randBetween, randInt, uid } from './util'

let state: MockState = buildInitialState()

export function resetState(): void {
  state = buildInitialState()
}

export function getState(): MockState {
  return state
}

// ── Pub/sub (used for live deploy-log frames) ───────────────────────────────

type Listener = (frame: WsFrame) => void
const topics = new Map<string, Set<Listener>>()

export function subscribe(topic: string, fn: Listener): () => void {
  let set = topics.get(topic)
  if (!set) {
    set = new Set()
    topics.set(topic, set)
  }
  set.add(fn)
  return () => set?.delete(fn)
}

function publish(topic: string, frame: WsFrame): void {
  topics.get(topic)?.forEach((fn) => fn(frame))
}

export const deployLogTopic = (did: string) => `deploy-log:${did}`

// ── Activity feed / curated revert (plan 11) ────────────────────────────────

export function listAudit(limit: number): AuditEntry[] {
  const cap = Math.max(1, Math.min(limit || 100, 500))
  return (
    [...state.audit]
      .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
      .slice(0, cap)
      // Mirror the real DTO: an entry is offered for revert only while it is both
      // revertable and not yet reverted. The stored flag itself never flips.
      .map((e) => ({ ...e, revertable: e.revertable && !e.reverted_at }))
  )
}

export type RevertOutcome =
  | { status: 'ok'; summary: string }
  | { status: 'not_found' | 'conflict' | 'not_revertable' }

// revertAudit mirrors the real backend: it marks the entry reverted, then
// appends the revert's own (non-revertable) audit entry.
export function revertAudit(id: string): RevertOutcome {
  const entry = state.audit.find((e) => e.id === id)
  if (!entry) return { status: 'not_found' }
  if (entry.reverted_at) return { status: 'conflict' }
  if (!entry.revertable) return { status: 'not_revertable' }
  // Stamp reverted_at only; the stored revertable flag stays put, matching the
  // backend (the DTO derives the display value from both — see listAudit).
  entry.reverted_at = nowISO()
  const summary = revertSummary(entry)
  state.audit.unshift({
    id: uid('aud'),
    actor_type: 'user',
    actor: state.user.username,
    action: `POST /api/audit/${id}/revert`,
    target_type: 'audit_log',
    target_id: id,
    summary: `reverted: ${summary}`,
    status_code: 200,
    revertable: false,
    has_preview: false,
    created_at: nowISO(),
  })
  return { status: 'ok', summary }
}

// curatedKind maps an audit action to the diff kind it previews, or null for a
// non-curated action (mirrors the backend's auditdiff dispatch).
function curatedKind(action: string): ActivityDiff['kind'] | null {
  if (/PUT .*\/apps\/\{id\}\/env$/.test(action)) return 'env'
  if (/PATCH .*\/apps\/\{id\}$/.test(action)) return 'app'
  if (/PUT .*\/instance\/base-domain$/.test(action)) return 'base_domain'
  return null
}

export type DiffOutcome =
  | { status: 'ok'; diff: ActivityDiff }
  | { status: 'not_found' | 'not_diffable' }

// auditDiff returns a representative before→current diff for a curated entry,
// driven off the entry's action so the kind matches. Mirrors the real endpoint:
// secrets are masked, never sent as plaintext.
export function auditDiff(id: string): DiffOutcome {
  const entry = state.audit.find((e) => e.id === id)
  if (!entry) return { status: 'not_found' }
  const kind = curatedKind(entry.action)
  if (!kind) return { status: 'not_diffable' }
  return {
    status: 'ok',
    diff: { ...DIFF_FIXTURES[kind], current_as_of: nowISO(), changed_since: false },
  }
}

const DIFF_FIXTURES: Record<
  ActivityDiff['kind'],
  Omit<ActivityDiff, 'current_as_of' | 'changed_since'>
> = {
  env: {
    kind: 'env',
    rows: [
      {
        label: 'DATABASE_URL',
        status: 'changed',
        before: 'postgres://old',
        after: 'postgres://new',
        masked: false,
      },
      { label: 'FEATURE_FLAG', status: 'added', after: 'true', masked: false },
      { label: 'LEGACY_KEY', status: 'removed', before: 'gone', masked: false },
      { label: 'API_SECRET', status: 'changed', masked: true },
      { label: 'LOG_LEVEL', status: 'unchanged', before: 'info', after: 'info', masked: false },
    ],
  },
  app: {
    kind: 'app',
    rows: [
      {
        label: 'Name',
        status: 'changed',
        before: 'Marketing',
        after: 'Marketing Site',
        masked: false,
      },
      { label: 'Branch', status: 'changed', before: 'main', after: 'release', masked: false },
      {
        label: 'Memory limit',
        status: 'changed',
        before: '256 MB',
        after: 'unlimited',
        masked: false,
      },
    ],
  },
  base_domain: {
    kind: 'base_domain',
    rows: [
      {
        label: 'Base domain',
        status: 'changed',
        before: 'apps.example.com',
        after: '(cleared)',
        masked: false,
      },
    ],
  },
}

function revertSummary(entry: AuditEntry): string {
  if (entry.action.endsWith('/env')) return 'restored previous environment variables'
  if (entry.action.endsWith('/base-domain')) return 'restored previous base domain'
  if (/PATCH .*\/apps\/\{id\}$/.test(entry.action)) return 'restored previous app configuration'
  return 'restored previous state'
}

// ── Lookups ─────────────────────────────────────────────────────────────────

export function findApp(id: string): AppRecord | undefined {
  return state.apps.find((a) => a.id === id || a.slug === id)
}

export function findDeployment(did: string): { app: AppRecord; dep: DeployRecord } | undefined {
  for (const app of state.apps) {
    const dep = app.deployments.find((d) => d.id === did)
    if (dep) return { app, dep }
  }
  return undefined
}

// Strip the server-side log buffer so the wire shape matches the real API.
export function toPublicDeployment(d: DeployRecord): Deployment {
  const { logs: _logs, ...pub } = d
  void _logs
  return pub
}

// ── App CRUD + stack control ────────────────────────────────────────────────

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
}

export function createApp(input: CreateAppInput): AppRecord {
  const id = uid('app')
  const app: AppRecord = {
    id,
    name: input.name,
    slug: input.slug?.trim() || slugify(input.name),
    git_url: input.git_url,
    git_branch: input.git_branch?.trim() || 'main',
    compose_file: input.compose_file ?? 'compose.yaml',
    build_kind: input.build_kind ?? 'auto',
    build_config: input.build_config ?? {},
    status: 'stopped',
    mem_limit_mb: null,
    disk_limit_mb: null,
    created_at: nowISO(),
    updated_at: nowISO(),
    source: 'git',
    template_id: null,
    services: [],
    deployments: [],
    env: [],
    domains: [],
  }
  state.apps.unshift(app)
  return app
}

export function updateApp(app: AppRecord, input: UpdateAppInput): AppRecord {
  if (input.name !== undefined) app.name = input.name
  if (input.git_url !== undefined) app.git_url = input.git_url
  if (input.git_branch !== undefined) app.git_branch = input.git_branch
  if (input.compose_file !== undefined) app.compose_file = input.compose_file
  if (input.build_kind !== undefined) app.build_kind = input.build_kind
  if (input.build_config !== undefined) app.build_config = input.build_config
  app.updated_at = nowISO()
  return app
}

export function deleteApp(id: string): boolean {
  const i = state.apps.findIndex((a) => a.id === id || a.slug === id)
  if (i === -1) return false
  state.apps.splice(i, 1)
  return true
}

export function setStackState(app: AppRecord, action: 'start' | 'stop' | 'restart'): AppStatus {
  const status: AppStatus = action === 'stop' ? 'stopped' : 'running'
  app.status = status
  app.updated_at = nowISO()
  for (const s of app.services) {
    s.status = action === 'stop' ? 'stopped' : 'running'
    s.container_id = action === 'stop' ? null : uid('c').replace('c_', '')
    s.updated_at = nowISO()
  }
  return status
}

export function restartService(app: AppRecord, name: string): Service | undefined {
  const s = app.services.find((x) => x.name === name)
  if (!s) return undefined
  s.status = 'running'
  s.restart_count += 1
  s.last_exit_code = null
  s.container_id = uid('c').replace('c_', '')
  s.updated_at = nowISO()
  return s
}

export function updateService(
  app: AppRecord,
  name: string,
  input: { exposed_port?: number; internal_port?: number; health_path?: string },
): Service | undefined {
  const s = app.services.find((x) => x.name === name)
  if (!s) return undefined
  if (input.exposed_port !== undefined) s.exposed_port = input.exposed_port
  if (input.internal_port !== undefined) s.internal_port = input.internal_port
  if (input.health_path !== undefined) s.health_path = input.health_path
  s.updated_at = nowISO()
  return s
}

// ── Env vars ────────────────────────────────────────────────────────────────

export function listEnv(app: AppRecord): EnvVar[] {
  return app.env.map((e) =>
    e.sensitive
      ? { key: e.key, sensitive: true }
      : { key: e.key, sensitive: false, value: e.value },
  )
}

export function replaceEnv(app: AppRecord, vars: EnvVarInput[]): number {
  app.env = vars.map((v) => ({ key: v.key, value: v.value, sensitive: v.sensitive }))
  app.updated_at = nowISO()
  return app.env.length
}

export function revealEnv(app: AppRecord, key: string): EnvVar | undefined {
  const e = app.env.find((x) => x.key === key)
  if (!e) return undefined
  return { key: e.key, sensitive: e.sensitive, value: e.value }
}

// ── Domains ─────────────────────────────────────────────────────────────────

export function createDomain(app: AppRecord, service: string, hostname: string): Domain {
  const d: Domain = {
    id: uid('dom'),
    app_id: app.id,
    service_name: service,
    hostname,
    type: 'custom',
    managed: false,
    status: 'issuing',
    created_at: nowISO(),
    updated_at: nowISO(),
  }
  app.domains.push(d)
  // DNS/cert "settles" shortly after creation.
  setTimeout(() => {
    d.status = 'active'
    d.updated_at = nowISO()
  }, 4_000)
  return d
}

// Domains added in the hub without an app binding live here (the real backend
// stores them with NULL app_id/service_name).
let unassignedDomains: Domain[] = []

export function listAllDomains(): Domain[] {
  return [...state.apps.flatMap((a) => a.domains), ...unassignedDomains]
}

export function addDomainHub(hostname: string, appId?: string, service?: string): Domain {
  if (appId && service) {
    const app = findApp(appId)
    if (app) return createDomain(app, service, hostname)
  }
  const d: Domain = {
    id: uid('dom'),
    app_id: '',
    service_name: '',
    hostname,
    type: 'custom',
    managed: false,
    status: 'awaiting_dns',
    created_at: nowISO(),
    updated_at: nowISO(),
  }
  unassignedDomains.push(d)
  return d
}

function findDomainEverywhere(id: string): Domain | undefined {
  for (const app of state.apps) {
    const d = app.domains.find((x) => x.id === id)
    if (d) return d
  }
  return unassignedDomains.find((x) => x.id === id)
}

export function updateDomainHub(
  id: string,
  body: { hostname?: string; app_id?: string; service_name?: string; redirect_to?: string },
): Domain | undefined {
  const d = findDomainEverywhere(id)
  if (!d) return undefined
  if (body.hostname) d.hostname = body.hostname
  d.redirect_to = body.redirect_to || undefined
  // Move between assigned/unassigned buckets.
  const fromApp = state.apps.find((a) => a.domains.includes(d))
  if (fromApp) fromApp.domains = fromApp.domains.filter((x) => x !== d)
  else unassignedDomains = unassignedDomains.filter((x) => x !== d)
  d.app_id = body.app_id ?? ''
  d.service_name = body.service_name ?? ''
  if (d.app_id && d.service_name) {
    const app = findApp(d.app_id)
    if (app) app.domains.push(d)
  } else {
    unassignedDomains.push(d)
  }
  d.updated_at = nowISO()
  return d
}

export function deleteDomainById(id: string): boolean {
  const before = unassignedDomains.length
  unassignedDomains = unassignedDomains.filter((x) => x.id !== id)
  if (unassignedDomains.length < before) return true
  for (const app of state.apps) {
    const i = app.domains.findIndex((x) => x.id === id)
    if (i >= 0) {
      app.domains.splice(i, 1)
      return true
    }
  }
  return false
}

export function refreshDomainStatus(hostname: string): { state: string } | undefined {
  const d = listAllDomains().find((x) => x.hostname === hostname)
  if (!d) return undefined
  // Pretend a refresh nudges a settling domain toward active.
  if (d.status && d.status !== 'active')
    d.status = d.status === 'awaiting_dns' ? 'issuing' : 'active'
  return { state: d.status ?? 'checking' }
}

export function deleteDomain(app: AppRecord, domainId: string): boolean {
  const i = app.domains.findIndex((d) => d.id === domainId)
  if (i === -1) return false
  app.domains.splice(i, 1)
  return true
}

// ── Metrics & stats ─────────────────────────────────────────────────────────

export function hostStats(): HostStats {
  return {
    cpu_percent: Math.round(randBetween(6, 34) * 10) / 10,
    mem_used_bytes: Math.round(randBetween(0.9, 1.4) * 1024 ** 3),
    mem_total_bytes: 2 * 1024 ** 3,
    disk_used_bytes: 18 * 1024 ** 3,
    disk_total_bytes: 40 * 1024 ** 3,
    request_rate: Math.round(randBetween(2, 40)),
    host_ip: '203.0.113.7',
  }
}

export function appMetrics(_app: AppRecord, points = 60): MetricSample[] {
  const out: MetricSample[] = []
  const step = 60_000 // 1 minute between points
  const end = Date.now()
  for (let i = points - 1; i >= 0; i -= 1) {
    const requests = randInt(20, 220)
    out.push({
      ts: new Date(end - i * step).toISOString(),
      requests,
      errors: Math.random() < 0.25 ? randInt(0, 6) : 0,
      bytes_out: requests * randInt(800, 4000),
    })
  }
  return out
}

export function serviceStats(): ServiceStatsData {
  return {
    cpu_percent: Math.round(randBetween(1, 45) * 10) / 10,
    mem_bytes: Math.round(randBetween(40, 280) * 1024 ** 2),
    mem_percent: Math.round(randBetween(3, 28) * 10) / 10,
    net_rx_bytes: randInt(1_000, 900_000),
    net_tx_bytes: randInt(1_000, 900_000),
    uptime_seconds: randInt(3_600, 1_200_000),
  }
}

const RUNTIME_LOG_SAMPLES = [
  { stream: 'stdout', message: 'GET /api/products 200 12ms' },
  { stream: 'stdout', message: 'GET /api/cart 200 8ms' },
  { stream: 'stdout', message: 'POST /api/checkout 201 142ms' },
  { stream: 'stdout', message: 'cache hit: products:featured' },
  { stream: 'stderr', message: 'WARN upstream slow response (842ms)' },
  { stream: 'stdout', message: 'background job processed (queue=emails)' },
]

export function runtimeLogFrame(service: string | null): WsFrame {
  const sample = pick(RUNTIME_LOG_SAMPLES)
  return {
    type: 'log',
    ts: nowISO(),
    service: service ?? undefined,
    data: { stream: sample.stream, message: sample.message },
  }
}

// ── Deploy lifecycle ─────────────────────────────────────────────────────────

interface Stage {
  status: DeploymentStatus
  lines: { stream: string; message: string }[]
}

function buildStages(app: AppRecord): Stage[] {
  const slug = app.slug
  const primary = app.services[0]?.name ?? 'web'
  return [
    {
      status: 'cloning',
      lines: [
        { stream: 'stdout', message: `Cloning ${app.git_url} (branch ${app.git_branch})…` },
        { stream: 'stdout', message: 'Enumerating objects: done.' },
        { stream: 'stdout', message: `HEAD is now at ${fakeSha().slice(0, 7)}` },
      ],
    },
    {
      status: 'building',
      lines: [
        { stream: 'stdout', message: '#1 [internal] load build definition from Dockerfile' },
        { stream: 'stdout', message: '#5 [build 2/5] RUN npm ci' },
        { stream: 'stdout', message: 'added 412 packages in 6s' },
        { stream: 'stdout', message: '#6 [build 3/5] RUN npm run build' },
        { stream: 'stdout', message: '#9 exporting to image done' },
      ],
    },
    {
      status: 'deploying',
      lines: [
        { stream: 'stdout', message: 'Creating containers…' },
        { stream: 'stdout', message: `Attaching ${slug}--${primary} to vac-edge` },
        { stream: 'stdout', message: `Container ${slug}--${primary} started` },
      ],
    },
    {
      status: 'health-checking',
      lines: [
        { stream: 'stdout', message: 'Waiting for upstreams to report healthy…' },
        { stream: 'stdout', message: `Caddy reports ${slug}--${primary} healthy` },
      ],
    },
    {
      status: 'running',
      lines: [{ stream: 'stdout', message: 'Deployment live ✓' }],
    },
  ]
}

const COMMIT_MESSAGES = [
  'feat: add product recommendations',
  'fix: handle empty cart edge case',
  'perf: cache category listings',
  'chore: bump dependencies',
  'refactor: simplify checkout flow',
]

// Pushes a build log line into the deployment buffer and streams it live.
function pushBuildLine(did: string, dep: DeployRecord, stream: string, message: string): void {
  const line: DeploymentLogLine = {
    id: dep.logs.length + 1,
    service_name: null,
    stream,
    message,
    ts: nowISO(),
  }
  dep.logs.push(line)
  publish(deployLogTopic(did), {
    type: 'build',
    id: line.id,
    ts: line.ts,
    data: { stream: line.stream, message: line.message, service_name: line.service_name },
  })
}

export function triggerDeploy(app: AppRecord): Deployment {
  const did = uid('dep')
  const dep: DeployRecord = {
    id: did,
    app_id: app.id,
    status: 'queued',
    triggered_at: nowISO(),
    triggered_by: 'manual',
    rolled_back_from: null,
    started_at: null,
    finished_at: null,
    compose_hash: fakeSha().slice(0, 12),
    commit_sha: fakeSha(),
    commit_message: pick(COMMIT_MESSAGES),
    error: null,
    logs: [],
  }
  app.deployments.unshift(dep)
  app.status = 'building'
  app.updated_at = nowISO()

  // Drive the pipeline on timers. Each stage flips status, emits its lines,
  // then hands off to the next. The final stage marks the deploy terminal,
  // sets the app running, and emits build-end to close live log sockets.
  const stages = buildStages(app)
  let t = 400
  const lineGap = 220

  stages.forEach((stage) => {
    setTimeout(() => {
      dep.status = stage.status
      if (dep.started_at === null) dep.started_at = nowISO()
    }, t)
    t += 250
    stage.lines.forEach((l) => {
      setTimeout(() => pushBuildLine(did, dep, l.stream, l.message), t)
      t += lineGap
    })
    t += 200
  })

  setTimeout(() => {
    dep.status = 'running'
    dep.finished_at = nowISO()
    app.status = 'running'
    app.updated_at = nowISO()
    for (const s of app.services) {
      if (s.status !== 'running') {
        s.status = 'running'
        s.container_id = uid('c').replace('c_', '')
      }
    }
    publish(deployLogTopic(did), { type: 'build-end', ts: nowISO() })
  }, t + 200)

  return toPublicDeployment(dep)
}
