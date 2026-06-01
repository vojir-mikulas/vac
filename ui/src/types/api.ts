// Mirrors the Go API JSON contract (api/internal/server/handler/*).
// Field names match the `json:"..."` tags exactly.

export type AppStatus = 'running' | 'degraded' | 'stopped' | 'building' | 'crashed' | string

// Build adapters (mirrors api/internal/adapter). build_kind selects the adapter;
// build_config carries its kind-specific knobs (only relevant fields are set).
export type BuildKind = 'auto' | 'compose' | 'dockerfile' | 'framework' | 'static'

export interface BuildConfig {
  composePath?: string
  dockerfilePath?: string
  framework?: string
  buildCommand?: string
  startCommand?: string
  port?: number
  staticDir?: string
  spaFallback?: boolean
}

export interface App {
  id: string
  name: string
  slug: string
  git_url: string
  git_branch: string
  compose_file: string
  build_kind: BuildKind
  build_config: BuildConfig
  status: AppStatus
  created_at: string
  updated_at: string
}

export interface CreateAppInput {
  name: string
  slug?: string
  git_url: string
  git_branch?: string
  compose_file?: string
  build_kind?: BuildKind
  build_config?: BuildConfig
}

export interface UpdateAppInput {
  name?: string
  git_url?: string
  git_branch?: string
  compose_file?: string
  build_kind?: BuildKind
  build_config?: BuildConfig
}

export type ServiceStatus = 'running' | 'stopped' | 'crashed' | 'building' | string

export interface Service {
  id: string
  app_id: string
  name: string
  container_id: string | null
  exposed_port: number | null
  internal_port: number | null
  health_path: string | null
  status: ServiceStatus
  restart_count: number
  last_exit_code: number | null
  created_at: string
  updated_at: string
}

export interface UpdateServiceInput {
  exposed_port?: number
  internal_port?: number
  health_path?: string
}

// Mirrors api/internal/deploy/status.go. Terminal states are `running`
// (succeeded), `error` (failed), and `interrupted`. See lib/deploy-status.ts
// for the success/failed/active classifiers.
export type DeploymentStatus =
  | 'queued'
  | 'cloning'
  | 'building'
  | 'deploying'
  | 'health-checking'
  | 'running'
  | 'error'
  | 'interrupted'
  | string

export type DeploymentTrigger = 'manual' | 'push' | 'tag' | 'rollback' | 'system' | string

export interface Deployment {
  id: string
  app_id: string
  status: DeploymentStatus
  triggered_at: string
  triggered_by: DeploymentTrigger
  rolled_back_from: string | null
  started_at: string | null
  finished_at: string | null
  compose_hash: string | null
  commit_sha: string | null
  commit_message: string | null
  error: string | null
}

export interface DeploymentLogLine {
  id: number
  service_name: string | null
  stream: string
  message: string
  ts: string
}

// One env var as returned by the list endpoint. `value` is present only for
// non-sensitive keys; sensitive keys omit it and are revealed on demand.
export interface EnvVar {
  key: string
  sensitive: boolean
  value?: string
}

export interface Domain {
  id: string
  app_id: string
  service_name: string
  hostname: string
  type: string
  cert_status: string
  created_at: string
  updated_at: string
}

export interface MetricSample {
  ts: string
  requests: number
  errors: number
  bytes_out: number
}

export interface HostStats {
  cpu_percent: number
  mem_used_bytes: number
  mem_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  request_rate: number
  host_ip: string
}

export interface SSHKey {
  public_key: string
  fingerprint: string
  created_at: string
}

export interface TestConnectionResult {
  ok: boolean
  error_code?: string
  error_message?: string
}

// ── Auth ────────────────────────────────────────────────────────────────
export interface User {
  id: string
  username: string
  totp_enabled: boolean
}

export interface LoginInput {
  username: string
  password: string
  remember: boolean
}

export type LoginResult = User | { totp_required: true }

export function isTotpRequired(r: LoginResult): r is { totp_required: true } {
  return 'totp_required' in r && r.totp_required === true
}

export interface Session {
  id: string
  ip?: string
  user_agent?: string
  created_at: string
  last_seen_at: string
  expires_at: string
  is_current: boolean
}

export interface ApiToken {
  id: string
  name: string
  last_used_at: string | null
  created_at: string
  expires_at: string | null
}

export interface CreatedApiToken extends ApiToken {
  token: string
}

export interface TotpSetup {
  otpauth_uri: string
  secret: string
}

export interface SetupStatus {
  needs_setup: boolean
  token_required: boolean
}

// ── Notifications ─────────────────────────────────────────────────────────
export interface NotificationEvents {
  [event: string]: boolean
}

export interface NotificationSettings {
  discord_configured: boolean
  discord_hint: string
  slack_configured: boolean
  slack_hint: string
  events: NotificationEvents
}

export interface UpdateNotificationInput {
  discord_url?: string | null
  slack_url?: string | null
  events: NotificationEvents
}

// ── WebSocket frames ───────────────────────────────────────────────────────
export type WsFrameType = 'build' | 'build-end' | 'log' | 'stats' | 'host'

export interface WsFrame<T = unknown> {
  type: WsFrameType
  id?: number
  ts: string
  service?: string
  data?: T
}

export interface BuildLogData {
  stream: string
  message: string
  service_name: string | null
}

export interface RuntimeLogData {
  stream: string
  message: string
}

export interface ServiceStatsData {
  cpu_percent: number
  mem_bytes: number
  mem_percent: number
  net_rx_bytes: number
  net_tx_bytes: number
  uptime_seconds: number
}
