import { Link } from '@tanstack/react-router'
import { Boxes, ChevronDown, Database, Rocket, Settings } from 'lucide-react'

import { Meter } from '@/components/common/meter'
import { useHostStats } from '@/lib/api/metrics'
import { formatBytes, formatPercent } from '@/lib/format'

const NAV = [
  { to: '/apps', label: 'Apps', icon: Boxes },
  { to: '/deployments', label: 'Deployments', icon: Rocket },
  { to: '/database', label: 'Database', icon: Database },
  { to: '/settings', label: 'Settings', icon: Settings },
] as const

export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-svh w-sidebar shrink-0 flex-col border-r bg-surface-1 md:flex">
      <div className="border-b px-4.5 pb-3 pt-4.5">
        <Link to="/apps" className="flex w-full items-center gap-2.5">
          <img src="/vac-logo.svg" alt="" aria-hidden="true" className="size-7 rounded-md" />
          <div className="flex flex-col leading-tight">
            <span className="text-sm font-semibold tracking-tight">VAC</span>
            <span className="font-mono text-2xs text-muted-foreground">Containers</span>
          </div>
        </Link>
      </div>

      <ServerSelector />

      <nav className="flex flex-1 flex-col gap-px px-2 py-2.5">
        {NAV.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            className="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-normal text-muted-foreground transition-colors hover:bg-surface-2 hover:text-foreground data-[status=active]:bg-surface-2 data-[status=active]:font-medium data-[status=active]:text-foreground"
            activeProps={{ 'data-status': 'active' }}
          >
            <item.icon className="size-4" />
            <span>{item.label}</span>
          </Link>
        ))}
      </nav>

      <HostVitals />
    </aside>
  )
}

function ServerSelector() {
  return (
    <div className="px-3 pb-1.5 pt-3">
      <button
        type="button"
        className="flex w-full items-center gap-2.5 rounded-md border bg-background px-2.5 py-2 text-left"
      >
        <span className="size-2 shrink-0 rounded-full bg-ok shadow-[0_0_0_3px_color-mix(in_oklch,var(--ok),transparent_80%)]" />
        <div className="flex min-w-0 flex-1 flex-col leading-tight">
          <span className="truncate font-mono text-xs font-medium">localhost</span>
          <span className="text-2xs text-muted-foreground">this host</span>
        </div>
        <ChevronDown className="size-3.5 text-muted-foreground" />
      </button>
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
      <Meter pct={pct} className="h-0.5" />
    </div>
  )
}
