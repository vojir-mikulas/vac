import { AlertTriangle, CheckCircle2, ShieldAlert, XCircle } from 'lucide-react'
import { Trans, useTranslation } from 'react-i18next'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { EmptyState } from '@/components/common/empty-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { StatStripSkeleton } from '@/components/common/stat-strip-skeleton'
import { Card } from '@/components/ui/card'
import {
  useFail2ban,
  useFirewall,
  useSecurityPosture,
  useSecurityTraffic,
} from '@/lib/api/security'
import { relativeTime } from '@/lib/format'
import type { PostureFinding, RecentRequest, SecuritySeverity, TopTalker } from '@/types/api'

export function SecurityPage() {
  const { t } = useTranslation('security')
  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />

      <div className="mb-6">
        <PosturePanel />
      </div>

      <div className="mb-6">
        <TrafficPanel />
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Fail2banPanel />
        <FirewallPanel />
      </div>
    </PageContainer>
  )
}

// ── Posture ──────────────────────────────────────────────────────────────────

// Maps a posture severity to the StatusPill status token (its tone map).
const SEVERITY_STATUS: Record<SecuritySeverity, string> = {
  ok: 'success',
  warn: 'degraded',
  error: 'error',
}

function PosturePanel() {
  const { t } = useTranslation('security')
  const { data, isLoading } = useSecurityPosture()
  return (
    <>
      <SectionHeader>{t('posture.heading')}</SectionHeader>
      {isLoading ? (
        <ListSkeleton rows={5} avatar />
      ) : !data || data.length === 0 ? (
        <EmptyState title={t('posture.empty.title')} description={t('posture.empty.description')} />
      ) : (
        <>
          <PostureSummary findings={data} />
          <Card className="gap-0 p-0">
            {data.map((f, i) => (
              <PostureRow
                key={f.code + (f.app ?? '') + (f.service ?? '')}
                finding={f}
                first={i === 0}
              />
            ))}
          </Card>
        </>
      )}
    </>
  )
}

// PostureSummary is the at-a-glance banner that "lights up" red/amber/green from
// the worst finding, so an operator sees a problem without scanning every row.
function PostureSummary({ findings }: { findings: PostureFinding[] }) {
  const { t } = useTranslation('security')
  const errors = findings.filter((f) => f.severity === 'error').length
  const warns = findings.filter((f) => f.severity === 'warn').length
  const overall: SecuritySeverity = errors > 0 ? 'error' : warns > 0 ? 'warn' : 'ok'

  const tone =
    overall === 'error'
      ? 'border-err/40 bg-err/5'
      : overall === 'warn'
        ? 'border-warn/40 bg-warn/5'
        : 'border-ok/40 bg-ok/5'
  const headline =
    overall === 'error'
      ? t('posture.summary.needAttention', { count: errors })
      : overall === 'warn'
        ? t('posture.summary.warnings', { count: warns })
        : t('posture.summary.allPassing')
  const sub =
    overall === 'ok'
      ? t('posture.summary.checksPassing', { count: findings.length })
      : [
          errors ? t('posture.summary.errors', { count: errors }) : null,
          warns ? t('posture.summary.warnings', { count: warns }) : null,
        ]
          .filter(Boolean)
          .join(' · ')

  return (
    <Card className={`mb-3 flex flex-row items-center gap-4 border p-4 ${tone}`}>
      <div className="shrink-0">{severityIcon(overall)}</div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold">{headline}</div>
        <div className="text-2xs text-muted-foreground">{sub}</div>
      </div>
      <StatusPill status={SEVERITY_STATUS[overall]} size="sm" className="shrink-0" />
    </Card>
  )
}

function PostureRow({ finding, first }: { finding: PostureFinding; first: boolean }) {
  const scope = [finding.app, finding.service].filter(Boolean).join(' / ')
  return (
    <div className={`flex items-start gap-4 px-5 py-3.5 ${first ? '' : 'border-t'}`}>
      <div className="mt-0.5 shrink-0">{severityIcon(finding.severity)}</div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{finding.title}</span>
          {scope ? <span className="font-mono text-2xs text-muted-foreground">{scope}</span> : null}
        </div>
        <p className="mt-0.5 text-sm text-muted-foreground">{finding.message}</p>
      </div>
      <StatusPill
        status={SEVERITY_STATUS[finding.severity]}
        size="sm"
        className="mt-0.5 shrink-0"
      />
    </div>
  )
}

function severityIcon(s: SecuritySeverity) {
  if (s === 'error') return <XCircle className="size-4 text-err" />
  if (s === 'warn') return <AlertTriangle className="size-4 text-warn" />
  return <CheckCircle2 className="size-4 text-ok" />
}

// ── Traffic ──────────────────────────────────────────────────────────────────

function TrafficPanel() {
  const { t } = useTranslation('security')
  const { data, isLoading } = useSecurityTraffic()
  const windowLabel = data
    ? t('traffic.windowLabel', { seconds: data.window_seconds })
    : t('traffic.live')
  return (
    <>
      <SectionHeader>{t('traffic.heading')}</SectionHeader>
      {isLoading ? (
        <div className="flex flex-col gap-4">
          <StatStripSkeleton />
          <ListSkeleton rows={4} />
        </div>
      ) : (
        <>
          <div className="mb-4">
            <StatStrip>
              <StatTile
                label={t('traffic.stats.trackedIps')}
                value={String(data?.tracked_ips ?? 0)}
                sub={windowLabel}
                accent
              />
              <StatTile
                label={t('traffic.stats.requests')}
                value={String(data?.total_requests ?? 0)}
                sub={windowLabel}
              />
              <StatTile
                label={t('traffic.stats.errors')}
                value={String(data?.total_errors ?? 0)}
                sub={windowLabel}
              />
              <StatTile
                label={t('traffic.stats.recentAnomalies')}
                value={String(data?.recent_anomalies.length ?? 0)}
                sub={t('traffic.stats.thisProcess')}
              />
            </StatStrip>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div>
              <SectionHeader>{t('traffic.topTalkers.heading')}</SectionHeader>
              <TopTalkersTable talkers={data?.top_talkers ?? []} />
            </div>
            <div>
              <SectionHeader>{t('traffic.anomalies.heading')}</SectionHeader>
              <AnomaliesList anomalies={data?.recent_anomalies ?? []} />
            </div>
          </div>

          <div className="mt-6">
            <SectionHeader>{t('traffic.recentRequests.heading')}</SectionHeader>
            <RecentRequestsTable requests={data?.recent_requests ?? []} />
          </div>
        </>
      )}
    </>
  )
}

function RecentRequestsTable({ requests }: { requests: RecentRequest[] }) {
  const { t } = useTranslation('security')
  if (requests.length === 0) {
    return (
      <EmptyState
        title={t('traffic.recentRequests.empty.title')}
        description={t('traffic.recentRequests.empty.description')}
      />
    )
  }
  return (
    <Card className="gap-0 overflow-hidden p-0">
      <div className="flex items-center gap-3 border-b px-5 py-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        <span className="w-14 shrink-0">{t('traffic.recentRequests.status')}</span>
        <span className="w-14 shrink-0">{t('traffic.recentRequests.method')}</span>
        <span className="min-w-0 flex-1">{t('traffic.recentRequests.hostPath')}</span>
        <span className="w-28 shrink-0 text-right">{t('traffic.recentRequests.sourceIp')}</span>
        <span className="w-16 shrink-0 text-right">{t('traffic.recentRequests.when')}</span>
      </div>
      <div className="max-h-96 overflow-y-auto">
        {requests.map((r, i) => (
          <div
            key={r.at + r.ip + r.path + i}
            className={`flex items-center gap-3 px-5 py-2 ${i > 0 ? 'border-t' : ''}`}
          >
            <span
              className={`w-14 shrink-0 font-mono text-xs tabular-nums ${statusTone(r.status)}`}
            >
              {r.status}
            </span>
            <span className="w-14 shrink-0 font-mono text-2xs text-muted-foreground">
              {r.method}
            </span>
            <div className="min-w-0 flex-1">
              <div className="truncate font-mono text-xs">
                <span className="text-muted-foreground">{r.host}</span>
                {r.path}
              </div>
            </div>
            <span className="w-28 shrink-0 truncate text-right font-mono text-2xs text-muted-foreground">
              {r.ip}
            </span>
            <span className="w-16 shrink-0 text-right text-2xs text-muted-foreground">
              {relativeTime(r.at)}
            </span>
          </div>
        ))}
      </div>
    </Card>
  )
}

// statusTone colours an HTTP status: 2xx ok, 3xx muted, 4xx warn, 5xx error.
function statusTone(status: number): string {
  if (status >= 500) return 'text-err'
  if (status >= 400) return 'text-warn'
  if (status >= 300) return 'text-muted-foreground'
  return 'text-ok'
}

function TopTalkersTable({ talkers }: { talkers: TopTalker[] }) {
  const { t } = useTranslation('security')
  if (talkers.length === 0) {
    return (
      <EmptyState
        title={t('traffic.topTalkers.empty.title')}
        description={t('traffic.topTalkers.empty.description')}
      />
    )
  }
  return (
    <Card className="gap-0 p-0">
      <div className="flex items-center gap-4 border-b px-5 py-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        <span className="flex-1">{t('traffic.topTalkers.sourceIp')}</span>
        <span className="w-16 shrink-0 text-right">{t('traffic.topTalkers.reqs')}</span>
        <span className="w-16 shrink-0 text-right">{t('traffic.topTalkers.errors')}</span>
      </div>
      {talkers.map((t, i) => (
        <div key={t.ip} className={`flex items-center gap-4 px-5 py-3 ${i > 0 ? 'border-t' : ''}`}>
          <div className="min-w-0 flex-1">
            <div className="truncate font-mono text-xs font-medium">{t.ip}</div>
            {t.user_agent ? (
              <div className="truncate text-2xs text-muted-foreground">{t.user_agent}</div>
            ) : null}
          </div>
          <span className="w-16 shrink-0 text-right font-mono text-sm tabular-nums">
            {t.requests}
          </span>
          <span
            className={`w-16 shrink-0 text-right font-mono text-sm tabular-nums ${t.errors > 0 ? 'text-err' : 'text-muted-foreground'}`}
          >
            {t.errors}
          </span>
        </div>
      ))}
    </Card>
  )
}

function AnomaliesList({
  anomalies,
}: {
  anomalies: { at: string; ip: string; kind: string; detail: string }[]
}) {
  const { t } = useTranslation('security')
  if (anomalies.length === 0) {
    return (
      <EmptyState
        title={t('traffic.anomalies.empty.title')}
        description={t('traffic.anomalies.empty.description')}
      />
    )
  }
  return (
    <Card className="gap-0 p-0">
      {anomalies.map((a, i) => (
        <div
          key={a.at + a.ip + i}
          className={`flex items-start gap-3 px-5 py-3 ${i > 0 ? 'border-t' : ''}`}
        >
          <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warn" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium capitalize">{a.kind}</span>
              <span className="text-2xs text-muted-foreground">{relativeTime(a.at)}</span>
            </div>
            <p className="mt-0.5 text-sm text-muted-foreground">{a.detail}</p>
          </div>
        </div>
      ))}
    </Card>
  )
}

// ── fail2ban ─────────────────────────────────────────────────────────────────

function Fail2banPanel() {
  const { t } = useTranslation('security')
  const { data, isLoading } = useFail2ban()
  return (
    <div>
      <SectionHeader>{t('fail2ban.heading')}</SectionHeader>
      {isLoading ? (
        <ListSkeleton rows={3} />
      ) : !data?.source ? (
        <MonitoringOffState />
      ) : data.stale ? (
        <EmptyState
          title={t('fail2ban.staleEmpty.title')}
          description={t('fail2ban.staleEmpty.description')}
        />
      ) : !data.detected ? (
        <Card className="gap-1 border-warn/40 bg-warn/5 p-4">
          <div className="flex items-center gap-2">
            <AlertTriangle className="size-4 text-warn" />
            <span className="text-sm font-medium">{t('fail2ban.notDetected.title')}</span>
          </div>
          <p className="text-sm text-muted-foreground">{t('fail2ban.notDetected.description')}</p>
        </Card>
      ) : !data.jails || data.jails.length === 0 ? (
        <EmptyState
          title={t('fail2ban.noJails.title')}
          description={t('fail2ban.noJails.description')}
        />
      ) : (
        <>
          <Card className="gap-0 p-0">
            {data.jails.map((j, i) => (
              <div
                key={j.name}
                className={`flex flex-col gap-1 px-5 py-3 ${i > 0 ? 'border-t' : ''}`}
              >
                <div className="flex items-center justify-between">
                  <span className="font-mono text-sm font-medium">{j.name}</span>
                  <span className="text-2xs text-muted-foreground">
                    {t('fail2ban.banned', {
                      currentlyBanned: j.currently_banned,
                      totalBanned: j.total_banned,
                    })}
                  </span>
                </div>
                {j.banned_ips && j.banned_ips.length > 0 ? (
                  <div className="font-mono text-2xs text-muted-foreground">
                    {j.banned_ips.join(', ')}
                  </div>
                ) : null}
              </div>
            ))}
          </Card>
          <HostSourceFooter source={data.source} generatedAt={data.generated_at} />
        </>
      )}
    </div>
  )
}

// MonitoringOffState is shown when VAC has no host data at all (the opt-in host
// agent isn't enabled). Neutral, not alarming — the sandboxed control plane
// simply can't see host state until the operator opts in.
function MonitoringOffState() {
  const { t } = useTranslation('security')
  return (
    <EmptyState title={t('monitoringOff.title')} description={t('monitoringOff.description')} />
  )
}

// HostSourceFooter notes where the fail2ban/firewall read came from and how fresh
// it is — "via host agent · updated 12s ago" — so a stale/absent agent is legible.
function HostSourceFooter({ source, generatedAt }: { source?: string; generatedAt?: string }) {
  const { t } = useTranslation('security')
  if (!source) return null
  const label = source === 'agent' ? t('hostSource.viaHostAgent') : t('hostSource.viaHost')
  return (
    <p className="mt-1.5 text-2xs text-muted-foreground">
      {t('hostSource.via', { label })}
      {generatedAt ? t('hostSource.updated', { time: relativeTime(generatedAt) }) : ''}
    </p>
  )
}

// ── Firewall ─────────────────────────────────────────────────────────────────

function FirewallPanel() {
  const { t } = useTranslation('security')
  const { data, isLoading } = useFirewall()
  return (
    <div>
      <SectionHeader>{t('firewall.heading')}</SectionHeader>
      {isLoading ? (
        <ListSkeleton rows={3} />
      ) : !data?.source ? (
        <MonitoringOffState />
      ) : data.stale ? (
        <EmptyState
          title={t('firewall.staleEmpty.title')}
          description={t('firewall.staleEmpty.description')}
        />
      ) : !data.detected ? (
        <Card className="gap-1 border-warn/40 bg-warn/5 p-4">
          <div className="flex items-center gap-2">
            <AlertTriangle className="size-4 text-warn" />
            <span className="text-sm font-medium">{t('firewall.notDetected.title')}</span>
          </div>
          <p className="text-sm text-muted-foreground">
            <Trans
              t={t}
              i18nKey="firewall.notDetected.description"
              components={[<code className="font-mono text-2xs" />]}
            />
          </p>
        </Card>
      ) : (
        <>
          <Card className="gap-2 p-4">
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-medium uppercase">{data.backend}</span>
              <StatusPill status={data.active ? 'running' : 'stopped'} size="sm" />
            </div>
            {data.rules && data.rules.length > 0 ? (
              <pre className="overflow-x-auto rounded-lg bg-surface-2 p-3 font-mono text-2xs leading-relaxed">
                {data.rules.join('\n')}
              </pre>
            ) : (
              <p className="text-sm text-muted-foreground">{t('firewall.noRules')}</p>
            )}
          </Card>
          <HostSourceFooter source={data.source} generatedAt={data.generated_at} />
        </>
      )}
    </div>
  )
}
