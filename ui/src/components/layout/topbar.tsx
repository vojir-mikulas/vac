import { Fragment } from 'react'
import { Link, useRouterState } from '@tanstack/react-router'
import { LogOut, Search } from 'lucide-react'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ThemeToggle } from '@/components/theme/theme-toggle'
import { useApps } from '@/lib/api/apps'
import { useLogout, useMe } from '@/lib/api/auth'

const STATIC_LABELS: Record<string, string> = {
  apps: 'Apps',
  new: 'New',
  deployments: 'Deployments',
  database: 'Database',
  logs: 'Logs',
  settings: 'Settings',
  overview: 'Overview',
  services: 'Services',
  deploys: 'Deploys',
  environment: 'Environment',
}

interface Crumb {
  label: string
  to?: string
  mono?: boolean
}

export function Topbar({ onOpenSearch }: { onOpenSearch: () => void }) {
  const crumbs = useBreadcrumbs()

  return (
    <header className="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-4 border-b bg-background/85 px-6 backdrop-blur">
      <nav className="flex min-w-0 flex-1 items-center gap-2 text-sm">
        {crumbs.map((c, i) => {
          const last = i === crumbs.length - 1
          return (
            <Fragment key={`${c.label}-${i}`}>
              {i > 0 ? <span className="text-muted-foreground">/</span> : null}
              {c.to && !last ? (
                <Link
                  to={c.to}
                  className={`text-muted-foreground hover:text-foreground ${c.mono ? 'font-mono' : ''}`}
                >
                  {c.label}
                </Link>
              ) : (
                <span
                  className={`${last ? 'font-medium text-foreground' : 'text-muted-foreground'} ${c.mono ? 'font-mono' : ''}`}
                >
                  {c.label}
                </span>
              )}
            </Fragment>
          )
        })}
      </nav>

      <button
        type="button"
        onClick={onOpenSearch}
        className="flex h-8 w-72 cursor-pointer items-center gap-2 rounded-md border bg-surface-1 px-3 text-muted-foreground transition-colors hover:border-border-strong"
      >
        <Search className="size-3.5" />
        <span className="flex-1 text-left text-xs">Search…</span>
        <kbd className="rounded border bg-background px-1.5 font-mono text-2xs">⌘K</kbd>
      </button>

      <ThemeToggle />
      <UserMenu />
    </header>
  )
}

function UserMenu() {
  const { data: me } = useMe()
  const logout = useLogout()
  const initials = (me?.username ?? '?').slice(0, 2).toUpperCase()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="grid size-8 cursor-pointer place-items-center rounded-full border bg-brand/15 font-sans text-xs font-semibold text-brand"
        >
          {initials}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel className="truncate">{me?.username ?? 'Account'}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => logout.mutate()}>
          <LogOut className="size-4" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function useBreadcrumbs(): Crumb[] {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const { data: apps } = useApps()

  const segments = pathname.split('/').filter(Boolean)
  if (segments.length === 0) return [{ label: 'Apps', to: '/apps' }]

  const crumbs: Crumb[] = []
  let acc = ''
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i]!
    acc += `/${seg}`

    // /apps/:appId — show the app name (mono), not the raw id.
    if (segments[0] === 'apps' && i === 1) {
      const app = apps?.find((a) => a.id === seg)
      crumbs.push({ label: app?.name ?? seg, to: acc, mono: true })
      continue
    }
    crumbs.push({ label: STATIC_LABELS[seg] ?? seg, to: acc })
  }
  return crumbs
}
