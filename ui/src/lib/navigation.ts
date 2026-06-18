import {
  Activity,
  Archive,
  Blocks,
  Boxes,
  Database,
  HardDrive,
  Rocket,
  ScrollText,
  Settings,
  ShieldCheck,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

import { useInstanceInfo } from '@/lib/api/instance'

// Single source of truth for top-level navigation, shared by the sidebar and the
// command palette so the two can never silently drift. `key` indexes into the
// `nav.*` i18n catalog; the label is resolved at render.
export interface NavItem {
  to: string
  key: string
  icon: LucideIcon
}

export const NAV = [
  { to: '/apps', key: 'apps', icon: Boxes },
  { to: '/deployments', key: 'deployments', icon: Rocket },
  { to: '/activity', key: 'activity', icon: Activity },
  { to: '/logs', key: 'logs', icon: ScrollText },
  { to: '/security', key: 'security', icon: ShieldCheck },
  { to: '/database', key: 'database', icon: Database },
  { to: '/storage', key: 'storage', icon: HardDrive },
  { to: '/settings', key: 'settings', icon: Settings },
] as const

// Shown only when the managed-services gate (Track D) is open.
export const ADDONS_NAV = { to: '/addons', key: 'addons', icon: Blocks } as const
export const BACKUPS_NAV = { to: '/backups', key: 'backups', icon: Archive } as const

// `key` stays a literal union so `t(`nav.${key}`)` resolves against the typed
// i18n catalog (the same trick the sidebar relies on).
export type NavEntry = (typeof NAV)[number] | typeof ADDONS_NAV | typeof BACKUPS_NAV

// Slot the managed-services surfaces (Add-ons, Backups) in around Database when
// the gate is open: … Security, Add-ons, Database, Backups, Settings.
export function navItems(managed: boolean) {
  return managed
    ? [...NAV.slice(0, 4), ADDONS_NAV, ...NAV.slice(4, 5), BACKUPS_NAV, ...NAV.slice(5)]
    : [...NAV]
}

// Hook form: reads the managed-services gate and returns the ordered nav list.
export function useNavItems() {
  const { data: instance } = useInstanceInfo()
  return navItems(instance?.managed_services ?? false)
}
