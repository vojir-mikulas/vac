import { AlertTriangle, CheckCircle2, ShieldAlert, XCircle } from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { StatStrip, StatTile } from '@/components/common/stat-tile'
import { StatusPill } from '@/components/common/status-pill'
import { EmptyState } from '@/components/common/empty-state'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  useFail2ban,
  useFirewall,
  useSecurityPosture,
  useSecurityTraffic,
} from '@/lib/api/security'
import { relativeTime } from '@/lib/format'
import type { PostureFinding, SecuritySeverity, TopTalker } from '@/types/api'

export function SecurityPage() {
  return (
    <PageContainer>
      <PageHeader
        title="Security"
        description="Read-only posture and traffic signals. VAC shows and alerts — it never mutates host state."
      />

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
  const { data, isLoading } = useSecurityPosture()
  return (
    <>
      <SectionHeader>Posture</SectionHeader>
      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : !data || data.length === 0 ? (
        <EmptyState title="No posture checks" description="The posture checklist is unavailable." />
      ) : (
        <Card className="gap-0 p-0">
          {data.map((f, i) => (
            <PostureRow
              key={f.code + (f.app ?? '') + (f.service ?? '')}
              finding={f}
              first={i === 0}
            />
          ))}
        </Card>
      )}
    </>
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
  const { data, isLoading } = useSecurityTraffic()
  const windowLabel = data ? `${data.window_seconds}s window` : 'live'
  return (
    <>
      <SectionHeader>Traffic</SectionHeader>
      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : (
        <>
          <div className="mb-4">
            <StatStrip>
              <StatTile
                label="Tracked IPs"
                value={String(data?.tracked_ips ?? 0)}
                sub={windowLabel}
                accent
              />
              <StatTile
                label="Requests"
                value={String(data?.total_requests ?? 0)}
                sub={windowLabel}
              />
              <StatTile
                label="Errors (4xx/5xx)"
                value={String(data?.total_errors ?? 0)}
                sub={windowLabel}
              />
              <StatTile
                label="Recent anomalies"
                value={String(data?.recent_anomalies.length ?? 0)}
                sub="this process"
              />
            </StatStrip>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div>
              <SectionHeader>Top talkers</SectionHeader>
              <TopTalkersTable talkers={data?.top_talkers ?? []} />
            </div>
            <div>
              <SectionHeader>Recent anomalies</SectionHeader>
              <AnomaliesList anomalies={data?.recent_anomalies ?? []} />
            </div>
          </div>
        </>
      )}
    </>
  )
}

function TopTalkersTable({ talkers }: { talkers: TopTalker[] }) {
  if (talkers.length === 0) {
    return <EmptyState title="No traffic" description="No requests seen in the current window." />
  }
  return (
    <Card className="gap-0 p-0">
      <div className="flex items-center gap-4 border-b px-5 py-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        <span className="flex-1">Source IP</span>
        <span className="w-16 shrink-0 text-right">Reqs</span>
        <span className="w-16 shrink-0 text-right">Errors</span>
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
  if (anomalies.length === 0) {
    return <EmptyState title="No anomalies" description="No traffic anomalies detected." />
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
  const { data, isLoading } = useFail2ban()
  return (
    <div>
      <SectionHeader>fail2ban</SectionHeader>
      {isLoading ? (
        <Skeleton className="h-32 w-full rounded-xl" />
      ) : !data?.detected ? (
        <EmptyState
          title="Not detected"
          description="fail2ban is not installed or readable on this host."
        />
      ) : !data.jails || data.jails.length === 0 ? (
        <EmptyState title="No jails" description="fail2ban is running but reports no jails." />
      ) : (
        <Card className="gap-0 p-0">
          {data.jails.map((j, i) => (
            <div
              key={j.name}
              className={`flex flex-col gap-1 px-5 py-3 ${i > 0 ? 'border-t' : ''}`}
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-sm font-medium">{j.name}</span>
                <span className="text-2xs text-muted-foreground">
                  {j.currently_banned} banned · {j.total_banned} total
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
      )}
    </div>
  )
}

// ── Firewall ─────────────────────────────────────────────────────────────────

function FirewallPanel() {
  const { data, isLoading } = useFirewall()
  return (
    <div>
      <SectionHeader>Firewall</SectionHeader>
      {isLoading ? (
        <Skeleton className="h-32 w-full rounded-xl" />
      ) : !data?.detected ? (
        <EmptyState
          title="Not detected"
          description="No ufw / nftables ruleset is readable on this host."
        />
      ) : (
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
            <p className="text-sm text-muted-foreground">No rules reported.</p>
          )}
        </Card>
      )}
    </div>
  )
}
