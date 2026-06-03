import { useId } from 'react'
import { m } from 'motion/react'
import { Link, useRouterState } from '@tanstack/react-router'
import {
  Activity,
  Blocks,
  Boxes,
  Database,
  Rocket,
  Server,
  Settings,
  ShieldCheck,
} from 'lucide-react'

import { Meter } from '@/components/common/meter'
import { transition } from '@/lib/motion'
import { useActiveDeployments } from '@/lib/api/deployments'
import { useHostStats } from '@/lib/api/metrics'
import { useInstanceInfo } from '@/lib/api/instance'
import { useSecurityAttention } from '@/lib/api/security'
import { formatBytes, formatPercent } from '@/lib/format'
import { cn } from '@/lib/utils'

const NAV = [
  { to: '/apps', label: 'Apps', icon: Boxes },
  { to: '/deployments', label: 'Deployments', icon: Rocket },
  { to: '/activity', label: 'Activity', icon: Activity },
  { to: '/security', label: 'Security', icon: ShieldCheck },
  { to: '/database', label: 'Database', icon: Database },
  { to: '/settings', label: 'Settings', icon: Settings },
] as const

// Shown only when the managed-services gate (Track D) is open.
const ADDONS_NAV = { to: '/addons', label: 'Add-ons', icon: Blocks } as const

export function Sidebar() {
  return (
    <aside className="sticky top-3 m-3 hidden h-[calc(100svh-1.5rem)] w-sidebar shrink-0 flex-col rounded-xl border bg-surface-1 md:flex">
      <SidebarContent />
    </aside>
  )
}

// Inner sidebar layout, shared by the fixed desktop rail and the mobile drawer.
// `onNavigate` lets the mobile drawer close itself when a link is tapped.
interface NavBadge {
  count: number
  label: string
  className: string
}

export function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const { data: instance } = useInstanceInfo()
  const { data: queue } = useActiveDeployments()
  const security = useSecurityAttention()
  const deployCount = queue?.length ?? 0

  // Which top-level section is active, derived from the path so the highlight pill
  // can glide to it. Prefix match handles nested routes (/apps/$appId → Apps).
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const isActive = (to: string) => pathname === to || pathname.startsWith(`${to}/`)
  // Namespace the shared layoutId per mounted sidebar (desktop rail vs mobile
  // drawer both render this), so their pills don't fight over one layoutId.
  const pillId = `sidebar-active-${useId()}`

  // Per-item attention badge: deploys in flight (brand) and unresolved security
  // posture findings (severity-toned, matching the page's "N issues" banner).
  const badgeFor = (to: string): NavBadge | null => {
    if (to === '/deployments' && deployCount > 0) {
      return {
        count: deployCount,
        label: `${deployCount} active`,
        className: 'bg-brand text-brand-foreground',
      }
    }
    if (to === '/security' && security.count > 0) {
      return {
        count: security.count,
        label:
          security.severity === 'error'
            ? `${security.count} issue${security.count === 1 ? '' : 's'} need attention`
            : `${security.count} warning${security.count === 1 ? '' : 's'}`,
        className:
          security.severity === 'error'
            ? 'bg-err text-err-foreground'
            : 'bg-warn text-warn-foreground',
      }
    }
    return null
  }

  // Slot Add-ons in before Settings when managed services are enabled.
  const nav = instance?.managed_services ? [...NAV.slice(0, 4), ADDONS_NAV, ...NAV.slice(4)] : NAV
  return (
    <>
      <div className="border-b px-4.5 pb-3 pt-4.5">
        <Link to="/apps" onClick={onNavigate} className="flex w-full items-center gap-2.5">
          <img src="/vac-logo.svg" alt="" aria-hidden="true" className="size-7 rounded-md" />
          <div className="flex flex-col leading-tight">
            <span className="text-sm font-semibold tracking-tight">VAC</span>
            <span className="font-mono text-2xs text-muted-foreground">
              Vojir's Awesome Containers
            </span>
          </div>
        </Link>
      </div>

      <HostIdentity />

      <nav aria-label="Primary" className="flex flex-1 flex-col gap-px px-2 py-2.5">
        {nav.map((item) => {
          const badge = badgeFor(item.to)
          const active = isActive(item.to)
          return (
            <Link
              key={item.to}
              to={item.to}
              onClick={onNavigate}
              className="relative flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-normal text-muted-foreground transition-colors hover:bg-surface-2 hover:text-foreground data-[status=active]:font-medium data-[status=active]:text-foreground"
              activeProps={{ 'data-status': 'active', 'aria-current': 'page' }}
            >
              {/* Active highlight: a single shared element that animates between
                  items via layoutId. Behind the icon/label, which sit relative. */}
              {active ? (
                <m.span
                  layoutId={pillId}
                  transition={transition.layout}
                  className="absolute inset-0 rounded-md bg-surface-2"
                />
              ) : null}
              <item.icon className="relative size-4" />
              <span className="relative flex-1">{item.label}</span>
              {badge ? (
                <span
                  aria-label={badge.label}
                  className={cn(
                    'relative grid min-w-5 place-items-center rounded-full px-1.5 text-2xs font-semibold leading-5',
                    badge.className,
                  )}
                >
                  {badge.count}
                </span>
              ) : null}
            </Link>
          )
        })}
      </nav>

      <HostVitals />
    </>
  )
}

function HostIdentity() {
  const { data } = useHostStats()
  const ip = data?.host_ip
  return (
    <div className="px-3 pb-1.5 pt-3">
      <div className="flex w-full items-center gap-2.5 rounded-lg border bg-background px-2.5 py-2 text-left">
        {/* Server chip with a corner "online" badge — the ring matches the
            card background so the dot reads as a notch cut into the icon. */}
        <span className="relative grid size-8 shrink-0 place-items-center rounded-md bg-ok-bg text-ok-foreground">
          <Server className="size-4" />
          <span className="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-background bg-ok shadow-[0_0_0_3px_color-mix(in_oklch,var(--ok),transparent_82%)]" />
        </span>
        <div className="flex min-w-0 flex-1 flex-col leading-tight">
          <span className="text-2xs font-medium uppercase tracking-wider text-muted-foreground">
            This host
          </span>
          <span className="truncate font-mono text-xs font-medium">{ip || 'localhost'}</span>
        </div>
      </div>
    </div>
  )
}

function HostVitals() {
  const { data } = useHostStats()
  const ramPct = data ? (data.mem_used_bytes / data.mem_total_bytes) * 100 : 0
  const diskPct = data ? (data.disk_used_bytes / data.disk_total_bytes) * 100 : 0

  return (
    <div className="border-t p-3">
      <div className="mb-2.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        Host
      </div>
      <div className="flex flex-col gap-2">
        <VitalRow
          label="CPU"
          value={data ? formatPercent(data.cpu_percent, 0) : '—'}
          pct={data?.cpu_percent ?? 0}
        />
        <VitalRow
          label="RAM"
          value={
            data
              ? `${formatBytes(data.mem_used_bytes)} / ${formatBytes(data.mem_total_bytes)}`
              : '—'
          }
          pct={ramPct}
        />
        <VitalRow
          label="Disk"
          value={
            data
              ? `${formatBytes(data.disk_used_bytes)} / ${formatBytes(data.disk_total_bytes)}`
              : '—'
          }
          pct={diskPct}
        />
      </div>
    </div>
  )
}

function VitalRow({ label, value, pct }: { label: string; value: string; pct: number }) {
  return (
    <div>
      <div className="mb-1 flex justify-between font-mono text-2xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="tabular-nums">{value}</span>
      </div>
      <Meter pct={pct} className="h-0.5" label={`${label} usage`} />
    </div>
  )
}
