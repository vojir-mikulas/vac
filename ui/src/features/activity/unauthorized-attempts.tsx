import { useTranslation } from 'react-i18next'
import { ShieldAlert } from 'lucide-react'

import { SectionHeader } from '@/components/common/section-header'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Card } from '@/components/ui/card'
import { useUnauthorizedAttempts } from '@/lib/api/security'
import { relativeTime } from '@/lib/format'
import type { SecurityAttempt } from '@/types/api'

// Distinct paths shown per source before collapsing into a "+N more" hint.
const PATHS_SHOWN = 3

// UnauthorizedAttempts surfaces the unauthenticated attempts diverted out of the
// activity feed — failed logins and probes to bogus endpoints — grouped by
// source IP so a scanner hammering the box reads as one noisy row, not a
// thousand. It needs no host agent (it's the control plane's own request
// stream), so it sits on the always-available Activity page rather than the
// host-gated Security dashboard.
export function UnauthorizedAttempts() {
  const { t } = useTranslation('activity')
  const { data, isLoading, isError, refetch } = useUnauthorizedAttempts()
  const groups = groupBySource(data ?? [])

  return (
    <div className="mt-8">
      <SectionHeader>{t('attempts.heading')}</SectionHeader>
      <SwapFade
        id={isLoading ? 'loading' : isError ? 'error' : groups.length === 0 ? 'empty' : 'list'}
      >
        {isLoading ? (
          <ListSkeleton rows={3} avatar />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : groups.length === 0 ? (
          <EmptyState
            title={t('attempts.empty.title')}
            description={t('attempts.empty.description')}
          />
        ) : (
          <>
            <p className="mb-3 text-2xs text-muted-foreground">
              {t('attempts.summary', {
                count: data?.length ?? 0,
                sources: t('attempts.sources', { count: groups.length }),
              })}
            </p>
            <Card className="gap-0 p-0">
              {groups.map((g, i) => (
                <SourceRow key={g.ip} group={g} first={i === 0} />
              ))}
            </Card>
          </>
        )}
      </SwapFade>
    </div>
  )
}

function SourceRow({ group, first }: { group: SourceGroup; first: boolean }) {
  const { t } = useTranslation('activity')
  const hidden = group.paths.length - PATHS_SHOWN
  return (
    <div className={`flex items-start gap-3 px-5 py-3 ${first ? '' : 'border-t'}`}>
      <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warn" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate font-mono text-sm font-medium">{group.ip}</span>
          {group.userAgent ? (
            <span className="truncate text-2xs text-muted-foreground">{group.userAgent}</span>
          ) : null}
        </div>
        <div className="mt-1 flex flex-wrap gap-1.5">
          {group.paths.slice(0, PATHS_SHOWN).map((p) => (
            <span
              key={p.method + p.path}
              className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-2xs text-muted-foreground"
            >
              <span className="text-warn">{p.status}</span> {p.method} {p.path}
            </span>
          ))}
          {hidden > 0 ? (
            <span className="px-1 py-0.5 text-2xs text-muted-foreground">
              {t('attempts.morePaths', { count: hidden })}
            </span>
          ) : null}
        </div>
      </div>
      <div className="shrink-0 text-right">
        <div className="font-mono text-sm tabular-nums text-warn">{group.count}</div>
        <div className="text-2xs text-muted-foreground">
          {t('attempts.lastSeen', { time: relativeTime(group.lastSeen) })}
        </div>
      </div>
    </div>
  )
}

interface SourceGroup {
  ip: string
  userAgent: string
  count: number
  lastSeen: string // ISO of the most recent attempt
  paths: { method: string; path: string; status: number }[] // distinct, recent first
}

// groupBySource collapses the flat (newest-first) attempt list into one row per
// source IP: total count, the most recent user-agent / timestamp, and the
// distinct method+path combos it hit. Sorted by count desc so the loudest
// offender leads. Attempts with no IP collapse under a single "unknown" bucket.
function groupBySource(attempts: SecurityAttempt[]): SourceGroup[] {
  const byIp = new Map<string, SourceGroup>()
  for (const a of attempts) {
    const ip = a.ip || 'unknown'
    let g = byIp.get(ip)
    if (!g) {
      // List is newest-first, so the first attempt seen for an IP is its latest.
      g = { ip, userAgent: a.user_agent, count: 0, lastSeen: a.at, paths: [] }
      byIp.set(ip, g)
    }
    g.count++
    if (!g.paths.some((p) => p.method === a.method && p.path === a.path)) {
      g.paths.push({ method: a.method, path: a.path, status: a.status })
    }
  }
  return [...byIp.values()].sort(
    (a, b) => b.count - a.count || b.lastSeen.localeCompare(a.lastSeen),
  )
}
