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
  /** Per-app RAM ceiling in MiB; null = unlimited / box default (plan 06). */
  mem_limit_mb: number | null
  created_at: string
  updated_at: string
  /** 'git' (clones a repo) or 'template' (an installed add-on). */
  source: 'git' | 'template'
  /** Add-on template id for template-sourced apps; null for git apps. */
  template_id: string | null
  /** Resolved add-on display name (template apps only). */
  template_name?: string
  /** Brand-icon key the UI maps to a glyph (template apps only). */
  template_icon?: string
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
  /** 0 clears the limit (unlimited); a positive value sets it in MiB. */
  mem_limit_mb?: number
}

// Result of importing a portable spec (plan 18). Mirrors
// portability.ImportResult. secrets_needed lists sensitive env keys imported
// without a value — the operator re-enters them after.
export interface ImportResult {
  app_id: string
  slug: string
  created: boolean
  services: number
  domains: number
  triggers: number
  env_vars: number
  secrets_needed?: string[]
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
  /** Times this service's container was OOM-killed (plan 06). */
  oom_killed_count: number
  /** True when the service declares a persistent volume — gates the backup nudge. */
  has_volumes: boolean
  created_at: string
  updated_at: string
}

/** Box-level RAM budget for the dashboard panel (GET /api/host/budget). */
export interface BoxBudget {
  total_ram_mb: number
  allocated_mb: number
  apps_with_limit: number
  apps_total: number
  over_committed: boolean
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
  | 'canceled'
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
  // A write-only secret can be set/replaced or deleted but never read back
  // (reveal → 403). Implies `sensitive`. The value is always omitted.
  write_only?: boolean
  value?: string
}

/** Live DNS/cert status projection (plan 09 F3). */
export type DomainStatusState =
  | 'checking'
  | 'awaiting_dns'
  | 'misconfigured'
  | 'issuing'
  | 'active'
  | 'error'

export interface Domain {
  id: string // "" for derived auto hosts (no backing row)
  app_id: string
  service_name: string
  hostname: string
  type: string // 'custom' | 'auto'
  managed: boolean // derived auto host — read-only
  redirect_to?: string // Phase 3: when set, 308-redirects to this host
  status?: DomainStatusState
  status_detail?: string
  cert_not_after?: string
  last_checked?: string
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

// Probe result for a repo's .env.example (new-app wizard pre-fill). `content` is
// the raw file text — the UI parses it client-side. `error_code` mirrors the
// test-connection codes (e.g. `auth_failed` for an unreachable private repo).
export interface EnvExampleResult {
  found: boolean
  file?: string
  content?: string
  error_code?: string
  error_message?: string
}

// Probe result for a repo's compose file (new-app wizard build-step pre-fill).
// `path` is the filename found at the repo root (e.g. `docker-compose.yml`).
// `error_code` mirrors the test-connection codes (e.g. `auth_failed` for an
// unreachable private repo), in which case the UI falls back to manual entry.
export interface ComposeDetectResult {
  found: boolean
  path?: string
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

// ── Track D: managed backups ───────────────────────────────────────────────
export type BackupFrequency = 'daily' | 'weekly'
export type BackupDestinationKind = 'local' | 's3'
export type BackupRunStatus = 'running' | 'success' | 'failed' | string

export interface BackupRun {
  id: string
  config_id: string
  started_at: string
  finished_at?: string | null
  status: BackupRunStatus
  size_bytes?: number | null
  artifact_key?: string | null
  error?: string | null
}

export interface BackupConfig {
  id: string
  app_id: string
  service_name: string
  command: string
  frequency: BackupFrequency
  hour_of_day: number
  day_of_week?: number | null
  destination: BackupDestinationKind
  keep_count: number
  enabled: boolean
  created_at: string
  updated_at: string
  last_run?: BackupRun | null
}

// S3 credentials are write-only — sent on create/update, never returned.
export interface S3DestinationInput {
  endpoint: string
  region?: string
  bucket: string
  access_key: string
  secret_key: string
  use_ssl?: boolean
  prefix?: string
}

export interface BackupConfigInput {
  service_name: string
  command: string
  frequency: BackupFrequency
  hour_of_day: number
  day_of_week?: number | null
  destination: BackupDestinationKind
  s3?: S3DestinationInput | null
  keep_count: number
  enabled?: boolean
}

// ── Track D: managed databases ─────────────────────────────────────────────
export type ManagedDBStatus = 'provisioning' | 'ready' | 'error' | string

export interface ManagedDatabase {
  id: string
  app_id: string
  engine: string
  db_name: string
  role_name?: string | null
  env_var_name: string
  status: ManagedDBStatus
  error?: string | null
  created_at: string
  footprint_mb: number
  shared: boolean
}

export interface DBEngineInfo {
  name: string
  footprint_mb: number
  shared: boolean
}

export interface AddDatabaseResult {
  database: ManagedDatabase
  warning?: string
}

// Box-wide database inventory (plan 20). One group per engine; the control-plane
// vac-db is a pinned, app-less entry on the Postgres group.
export interface DBBackupSummary {
  status: BackupRunStatus
  finished_at?: string | null
  size_bytes?: number | null
}

export interface DBInventoryEntry {
  id?: string
  app_id?: string
  app_slug?: string
  app_name?: string
  db_name: string
  env_var_name?: string
  status: string
  /** null = size unknown for this engine (never render as 0). */
  size_bytes: number | null
  last_backup?: DBBackupSummary | null
  is_control_plane: boolean
}

export interface DBEngineGroup {
  engine: string
  footprint_mb: number
  shared: boolean
  databases: DBInventoryEntry[]
}

export interface DatabaseInventory {
  engines: DBEngineGroup[]
}

// ── Track D: add-on catalog ────────────────────────────────────────────────
/**
 * "template" add-ons deploy as a normal app; "database" add-ons cross-list a
 * heavyweight managed-DB engine (e.g. MariaDB) — provisioned per app, not
 * deployed standalone.
 */
export type AddonKind = 'template' | 'database'

export interface Addon {
  kind: AddonKind
  id: string
  name: string
  description: string
  category: string
  /** Brand-icon key the UI maps to a glyph; "" falls back to a generic icon. */
  icon: string
  footprint_mb: number
  /** Managed-DB engine to provision before first deploy, or "" for none. */
  depends_on_db: string
  compose_file?: string
  default_env?: Record<string, string>
  /** Database add-ons: the engine runs as one shared instance across apps. */
  shared?: boolean
}

export interface AddonInstallResult {
  app_id: string
  slug: string
  name: string
  status: string
  deployment_id: string
  /** Secrets generated at install (e.g. an admin password), shown once. */
  generated_secrets?: Record<string, string>
}

// ── WebSocket frames ───────────────────────────────────────────────────────
export type WsFrameType = 'build' | 'build-end' | 'log' | 'stats' | 'host' | 'deployments'

/**
 * ActiveDeployment is one row in the instance-wide deploy-queue snapshot
 * (GET /deployments/active and the /deployments/stream WS): a Deployment plus
 * its app's display name and slug.
 */
export interface ActiveDeployment extends Deployment {
  app_name: string
  app_slug: string
}

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

// ── Security dashboard (plan 15 / E2) ────────────────────────────────────────
export type SecuritySeverity = 'ok' | 'warn' | 'error'

export interface PostureFinding {
  severity: SecuritySeverity
  code: string
  title: string
  message: string
  app?: string
  service?: string
}

export interface TopTalker {
  ip: string
  requests: number
  errors: number
  user_agent: string
  last_seen: string
}

export interface TrafficAnomaly {
  at: string
  ip: string
  kind: string
  detail: string
}

export interface RecentRequest {
  at: string
  ip: string
  host: string
  method: string
  path: string
  status: number
  user_agent: string
}

// SecurityAttempt is one unauthenticated attempt against the control plane —
// a failed login or a probe to a bogus path. Diverted out of the activity feed
// into its own record; surfaced on the Activity page.
export interface SecurityAttempt {
  id: string
  method: string
  path: string
  status: number
  ip: string
  user_agent: string
  at: string
}

export interface TrafficSnapshot {
  window_seconds: number
  tracked_ips: number
  total_requests: number
  total_errors: number
  top_talkers: TopTalker[]
  recent_requests: RecentRequest[]
  recent_anomalies: TrafficAnomaly[]
}

export interface Fail2banJail {
  name: string
  currently_banned: number
  total_banned: number
  banned_ips: string[] | null
}

export interface Fail2banState {
  detected: boolean
  jails: Fail2banJail[] | null
  stale: boolean
  // Where the read came from: "agent" (host collector snapshot) or "host"
  // (direct exec). Absent when neither path produced data.
  source?: string
  generated_at?: string
}

export interface FirewallState {
  detected: boolean
  backend: string
  active: boolean
  rules: string[] | null
  stale: boolean
  source?: string
  generated_at?: string
}
