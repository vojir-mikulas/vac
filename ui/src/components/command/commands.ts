import {
  ArrowRight,
  Bell,
  Boxes,
  Globe,
  KeyRound,
  Palette,
  Play,
  Plus,
  Power,
  RefreshCw,
  Rocket,
  RotateCw,
  Server,
  Square,
  SunMoon,
  TriangleAlert,
  UserCog,
} from 'lucide-react'
import type { TFunction } from 'i18next'

import type { App } from '@/types/api'
import type { NavEntry } from '@/lib/navigation'
import type { Command } from './types'

// Imperative navigation, injected so the builders stay pure and unit-testable.
export type Navigate = (to: string, params?: Record<string, string>) => void

type StackAction = 'start' | 'stop' | 'restart'

// ── Phase 1: top-level navigation, derived from the shared nav source ────────
export function navCommands(items: readonly NavEntry[], t: TFunction, go: Navigate): Command[] {
  return items.map((item) => ({
    id: `nav:${item.to}`,
    group: 'navigate',
    label: t(`nav.${item.key}`),
    icon: item.icon,
    perform: () => go(item.to),
  }))
}

// ── Phase 2: settings subpages ───────────────────────────────────────────────
const SETTINGS_PAGES = [
  { to: '/settings/appearance', key: 'appearance', icon: Palette },
  { to: '/settings/account', key: 'account', icon: UserCog },
  { to: '/settings/api-tokens', key: 'apiTokens', icon: KeyRound },
  { to: '/settings/notifications', key: 'notifications', icon: Bell },
  { to: '/settings/instance', key: 'instance', icon: Server },
  { to: '/settings/domains', key: 'domains', icon: Globe },
  { to: '/settings/deployments', key: 'deployments', icon: Rocket },
  { to: '/settings/danger', key: 'danger', icon: TriangleAlert },
] as const

export function settingsCommands(t: TFunction, go: Navigate): Command[] {
  const heading = t('nav.settings')
  return SETTINGS_PAGES.map((p) => ({
    id: `settings:${p.to}`,
    group: 'settings',
    label: t(`command.settings.${p.key}`),
    icon: p.icon,
    keywords: heading,
    perform: () => go(p.to),
  }))
}

// ── Phase 3: global actions ──────────────────────────────────────────────────
export interface ActionDeps {
  t: TFunction
  go: Navigate
  toggleTheme: () => void
  restartControlPlane: () => void
  stopAllApps: () => void
}

export function actionCommands(d: ActionDeps): Command[] {
  const { t } = d
  return [
    {
      id: 'action:new-app',
      group: 'actions',
      label: t('command.newApp'),
      icon: Plus,
      perform: () => d.go('/apps/new'),
    },
    {
      id: 'action:toggle-theme',
      group: 'actions',
      label: t('command.toggleTheme'),
      icon: SunMoon,
      perform: d.toggleTheme,
    },
    {
      id: 'action:restart-control-plane',
      group: 'actions',
      label: t('command.restartControlPlane'),
      icon: RotateCw,
      perform: d.restartControlPlane,
      confirm: {
        title: t('command.confirm.restartControlPlane.title'),
        description: t('command.confirm.restartControlPlane.description'),
        actionLabel: t('command.confirm.restartControlPlane.action'),
      },
    },
    {
      id: 'action:stop-all-apps',
      group: 'actions',
      label: t('command.stopAllApps'),
      icon: Power,
      perform: d.stopAllApps,
      confirm: {
        title: t('command.confirm.stopAllApps.title'),
        description: t('command.confirm.stopAllApps.description'),
        actionLabel: t('command.confirm.stopAllApps.action'),
      },
    },
  ]
}

// ── Dynamic per-app list (matches the pre-existing palette behaviour) ─────────
export function appListCommands(apps: App[] | undefined, go: Navigate): Command[] {
  if (!apps?.length) return []
  // Each app reachable directly by name or slug (in addition to the "Apps" page).
  return apps.map((app) => ({
    id: `app-list:${app.id}`,
    group: 'apps',
    label: app.name,
    icon: Boxes,
    keywords: `app ${app.name} ${app.slug}`,
    perform: () => go('/apps/$appId', { appId: app.id }),
  }))
}

// ── Phase 4: context-aware commands for the app currently being viewed ────────

// The union of tab slugs; `to` stays literal so `t(`command.tabs.${to}`)` resolves
// against the typed i18n catalog.
export type AppTab =
  | 'overview'
  | 'services'
  | 'deploys'
  | 'previews'
  | 'logs'
  | 'jobs'
  | 'environment'
  | 'backups'
  | 'databases'
  | 'settings'

// Tab paths for an app, mirroring the assembly in routes/_app/apps/$appId.tsx so
// the palette offers exactly the tabs the app actually shows.
export function appTabPaths(app: App, managed: boolean, hasManagedDB: boolean): AppTab[] {
  const tabs: AppTab[] = [
    'overview',
    'services',
    'deploys',
    'logs',
    'jobs',
    'environment',
    'settings',
  ]
  if (!app.is_preview) {
    tabs.splice(tabs.indexOf('deploys') + 1, 0, 'previews')
  }
  const isAddon = app.source === 'template'
  const showManagedTabs = managed && (!isAddon || hasManagedDB)
  if (showManagedTabs) {
    tabs.splice(tabs.indexOf('settings'), 0, 'backups', 'databases')
  }
  return tabs
}

export interface AppContextDeps {
  app: App
  managed: boolean
  hasManagedDB: boolean
  t: TFunction
  go: Navigate
  deploy: () => void
  stack: (action: StackAction) => void
}

export function appContextCommands(d: AppContextDeps): Command[] {
  const { app, t } = d
  const kw = `${app.name} ${app.slug}`
  const cmds: Command[] = [
    {
      id: 'app:deploy',
      group: 'app',
      label: t('command.deployNow'),
      icon: Rocket,
      keywords: kw,
      perform: d.deploy,
      confirm: {
        title: t('command.confirm.deployNow.title'),
        description: t('command.confirm.deployNow.description', { name: app.name }),
        actionLabel: t('command.confirm.deployNow.action'),
      },
    },
    {
      id: 'app:restart',
      group: 'app',
      label: t('command.restartStack'),
      icon: RefreshCw,
      keywords: kw,
      perform: () => d.stack('restart'),
      confirm: {
        title: t('command.confirm.restartStack.title'),
        description: t('command.confirm.restartStack.description', { name: app.name }),
        actionLabel: t('command.confirm.restartStack.action'),
      },
    },
  ]

  if (app.status === 'stopped') {
    cmds.push({
      id: 'app:start',
      group: 'app',
      label: t('command.startStack'),
      icon: Play,
      keywords: kw,
      perform: () => d.stack('start'),
    })
  } else {
    cmds.push({
      id: 'app:stop',
      group: 'app',
      label: t('command.stopStack'),
      icon: Square,
      keywords: kw,
      perform: () => d.stack('stop'),
      confirm: {
        title: t('command.confirm.stopStack.title'),
        description: t('command.confirm.stopStack.description', { name: app.name }),
        actionLabel: t('command.confirm.stopStack.action'),
      },
    })
  }

  // Tab navigation for the active app.
  for (const to of appTabPaths(app, d.managed, d.hasManagedDB)) {
    cmds.push({
      id: `app:tab:${to}`,
      group: 'app',
      label: t(`command.tabs.${to}`),
      icon: ArrowRight,
      keywords: kw,
      perform: () => d.go(`/apps/$appId/${to}`, { appId: app.id }),
    })
  }

  return cmds
}
