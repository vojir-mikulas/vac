import { Fragment, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useRouterState } from '@tanstack/react-router'
import { LogOut, Menu, Search } from 'lucide-react'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { SidebarContent } from '@/components/layout/sidebar'
import { ThemeToggle } from '@/components/theme/theme-toggle'
import { useApps } from '@/lib/api/apps'
import { useLogout, useMe } from '@/lib/api/auth'
import { useDocumentTitle } from '@/lib/use-document-title'

// Path segments with a localized label in the `crumbs.*` catalog. Unknown
// segments (app ids, etc.) fall back to the raw segment. The type guard narrows
// the segment to a literal so the `crumbs.${key}` lookup stays type-checked.
const CRUMB_KEYS = [
  'apps',
  'new',
  'deployments',
  'database',
  'logs',
  'settings',
  'overview',
  'services',
  'deploys',
  'environment',
] as const
type CrumbKey = (typeof CRUMB_KEYS)[number]
const isCrumbKey = (seg: string): seg is CrumbKey => (CRUMB_KEYS as readonly string[]).includes(seg)

interface Crumb {
  label: string
  to?: string
  mono?: boolean
}

export function Topbar({ onOpenSearch }: { onOpenSearch: () => void }) {
  const { t } = useTranslation()
  const crumbs = useBreadcrumbs()
  const [navOpen, setNavOpen] = useState(false)
  const current = crumbs[crumbs.length - 1]

  // Leaf-first trail, e.g. "Overview — myapp — Apps", so each route gets a
  // distinct, informative tab title.
  useDocumentTitle(
    crumbs
      .map((c) => c.label)
      .reverse()
      .join(' — '),
  )

  return (
    <header className="flex h-14 shrink-0 items-center gap-3 rounded-xl border bg-surface-1/85 px-3 backdrop-blur md:gap-4">
      <Sheet open={navOpen} onOpenChange={setNavOpen}>
        <SheetTrigger asChild>
          <button
            type="button"
            aria-label={t('topbar.openMenu')}
            className="grid size-9 shrink-0 cursor-pointer place-items-center rounded-md border bg-surface-1 text-muted-foreground transition-colors hover:text-foreground md:hidden"
          >
            <Menu className="size-4" />
          </button>
        </SheetTrigger>
        <SheetContent
          side="left"
          showCloseButton={false}
          className="w-sidebar gap-0 bg-surface-1 p-0"
        >
          <SheetTitle className="sr-only">{t('topbar.navigation')}</SheetTitle>
          <SidebarContent onNavigate={() => setNavOpen(false)} />
        </SheetContent>
      </Sheet>

      {/* Desktop: full breadcrumb trail. */}
      <nav
        aria-label={t('topbar.breadcrumb')}
        className="hidden min-w-0 flex-1 items-center gap-2 px-1 text-sm md:flex"
      >
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
                  aria-current={last ? 'page' : undefined}
                  className={`${last ? 'font-medium text-foreground' : 'text-muted-foreground'} ${c.mono ? 'font-mono' : ''}`}
                >
                  {c.label}
                </span>
              )}
            </Fragment>
          )
        })}
      </nav>

      {/* Mobile: just the current page, no breadcrumb trail. */}
      <span
        className={`min-w-0 flex-1 truncate text-sm font-medium md:hidden ${current?.mono ? 'font-mono' : ''}`}
      >
        {current?.label}
      </span>

      <button
        type="button"
        onClick={onOpenSearch}
        aria-label={t('topbar.searchAria')}
        className="flex h-8 w-8 shrink-0 cursor-pointer items-center justify-center rounded-md border bg-background text-muted-foreground transition-colors hover:border-border-strong hover:text-foreground md:w-72 md:justify-start md:gap-2 md:px-3"
      >
        <Search className="size-3.5" />
        <span className="hidden flex-1 text-left text-xs md:inline">{t('topbar.search')}</span>
        {/* Keyboard shortcut glyph — not translatable. */}
        {/* eslint-disable-next-line i18next/no-literal-string */}
        <kbd className="hidden rounded border bg-surface-2 px-1.5 font-mono text-2xs md:inline">
          ⌘K
        </kbd>
      </button>

      <span className="h-5 w-px bg-border" aria-hidden="true" />

      <ThemeToggle />
      <UserMenu />
    </header>
  )
}

function UserMenu() {
  const { t } = useTranslation()
  const { data: me } = useMe()
  const logout = useLogout()
  const initials = (me?.username ?? '?').slice(0, 2).toUpperCase()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t('topbar.accountMenu')}
          className="grid size-8 cursor-pointer place-items-center rounded-full bg-brand/15 font-sans text-xs font-semibold text-brand ring-1 ring-brand/25 transition-shadow hover:ring-brand/40"
        >
          {initials}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel className="truncate">
          {me?.username ?? t('topbar.account')}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => logout.mutate()}>
          <LogOut className="size-4" />
          {t('topbar.signOut')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function useBreadcrumbs(): Crumb[] {
  const { t } = useTranslation()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const { data: apps } = useApps()

  const segments = pathname.split('/').filter(Boolean)
  if (segments.length === 0) return [{ label: t('crumbs.apps'), to: '/apps' }]

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
    crumbs.push({ label: isCrumbKey(seg) ? t(`crumbs.${seg}`) : seg, to: acc })
  }
  return crumbs
}
