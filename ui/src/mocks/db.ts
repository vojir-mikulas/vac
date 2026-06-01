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
    created_at: nowISO(),
    updated_at: nowISO(),
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
    cert_status: 'provisioning',
    created_at: nowISO(),
    updated_at: nowISO(),
  }
  app.domains.push(d)
  // Certs "issue" shortly after creation.
  setTimeout(() => {
    d.cert_status = 'active'
    d.updated_at = nowISO()
  }, 4_000)
  return d
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
